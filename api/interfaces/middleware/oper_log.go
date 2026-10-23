package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"aiarsenal/domain/entity"
	"aiarsenal/domain/repository"
	"aiarsenal/domain/types"
)

// OperLog 写操作审计：非 GET 请求在执行后落一条 aa_oper_log。
//
// 挂载位置在 v1 根分组（CORS 之后、JWTAuth 之前）。中间件本身不发起鉴权，
// 只在 c.Next() 返回后读取 JWTAuth 写入的用户身份，因此对「未登录请求」不记录
// （登录、验证码、deviceCode 交换等，由各自的登录日志负责）。
//
// 三条设计约束：
//  1. 记录失败不能影响业务：Insert 出错只写 slog，不改响应。
//  2. 请求体要还原：读完请求体必须放回 c.Request.Body，否则处理器读到空 body。
//  3. 敏感字段不落库：密码、token、密钥在写库前掩码，multipart 只记占位符。
//
// 落库前还要按列宽裁剪（见 buildOperLog）：aa_oper_log 的 error_msg 是 varchar(2000)、
// title 是 varchar(50)，超长会让 INSERT 直接失败——那条审计就静默消失了，
// 而超长恰好集中在最该留痕的场景（长错误、批量失败）。
//
// 保留期与删除权限不在本中间件内解决：这里只保证单条有上限，
// 表增长与"谁能删日志"属于运维与日志表收权，随对应改动一起上。
const (
	auditMaxParamBytes = 2048     // 落库的请求参数上限
	auditMaxRespBytes  = 16 << 10 // 解析业务响应码时缓存的上限：够放 2000 个中文错误字符
	auditBodyReadLimit = 64 << 10
	// 500ms 不是拍脑袋：每个被审计的写接口本身就在本库写数据，
	// 审计只是同一连接池上多一次往返。留 500ms 兜住锁等待，而不是用 3 秒
	// 把一次 DB 抖动放大成写接口的 3 秒延迟。
	auditWriteTimeout = 500 * time.Millisecond
)

// 与 aa_oper_log 的列宽对齐（PostgreSQL varchar(n) 按字符计，不按字节）。
const (
	colTitleLen    = 50
	colMethodLen   = 100
	colOperNameLen = 50
	colOperURLLen  = 500
	colOperIPLen   = 50
	colErrorMsgLen = 2000
)

// auditSkipSuffixes 不记审计的写接口：机器心跳与运行时计量是周期性上报，
// 按设备或实例每几秒一次，入库会把审计表刷成噪音，真正的事件反而被淹没。
//
// 用后缀而不是完整路径：路由前缀调整（/api/v1 → /api/v2、分组改名）时，
// 写死完整路径会让跳过规则悄悄失效，心跳重新灌满审计表，而且没有任何报错。
var auditSkipSuffixes = []string{
	"/client/heartbeat",
	"/agent/runtime/heartbeat",
	"/agent/runtime/report",
}

func isAuditSkipped(fullPath string) bool {
	for _, suffix := range auditSkipSuffixes {
		if strings.HasSuffix(fullPath, suffix) {
			return true
		}
	}
	return false
}

// 若依约定的业务类型，前端按这个值渲染标签。
const (
	businessTypeOther  = 0 // 其它：exec / bind / clear 这类非 CRUD 写操作
	businessTypeAdd    = 1
	businessTypeEdit   = 2
	businessTypeDelete = 3
)

func OperLog(repo repository.IOperLogRepository, meta *AuditMeta) gin.HandlerFunc {
	if repo == nil {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		if !auditRequired(c) {
			c.Next()
			return
		}

		param := auditRequestParam(c)
		w := &auditResponseWriter{ResponseWriter: c.Writer, limit: auditMaxRespBytes}
		c.Writer = w

		// 用 defer 而不是顺序执行：处理器 panic 时也要留下这条审计。
		// panic 会一路穿到 Recovery，中间件里 defer 之后的语句不会执行，
		// 而"接口 500 了"恰恰是最需要留痕的时刻。
		defer func() {
			recovered := recover()
			entry := buildOperLog(c, w, param, meta)
			if entry != nil {
				if recovered != nil {
					// 不记 panic 内容的话，页面上会显示一条"成功"的记录，
					// 而实际请求返回了 500——比没有记录更容易误导排查。
					entry.ErrorMsg = clip("panic: "+fmt.Sprint(recovered), colErrorMsgLen)
				}
				insertOperLog(repo, entry)
			}
			if recovered != nil {
				panic(recovered) // 原样抛回，交还给 Recovery 处理
			}
		}()

		c.Next()
	}
}

// auditInsertFailures 累计写库失败次数。仓库里没有指标体系，
// 至少让每次失败日志带上累计值：运维能看出"偶发一条"还是"从某个时刻起全在丢"。
var (
	auditInsertFailures atomic.Int64
	auditInsertOK       atomic.Int64
	auditStatsAt        atomic.Int64 // 上次打统计日志的时间（unixnano）
)

// auditStatsInterval 统计日志间隔。它回答的是"审计还在写吗"——
// 没有这条，运维只能靠"页面看起来有数据"猜。
const auditStatsInterval = 5 * time.Minute

// maybeLogAuditStats 每 5 分钟在成功路径上打一条汇总：
// 不引入 goroutine、不依赖指标系统，grep 一行就能看趋势。
func maybeLogAuditStats() {
	now := time.Now().UnixNano()
	last := auditStatsAt.Load()
	if now-last < int64(auditStatsInterval) {
		return
	}
	if !auditStatsAt.CompareAndSwap(last, now) {
		return
	}
	slog.Info("审计写入统计",
		"ok", auditInsertOK.Load(),
		"failed", auditInsertFailures.Load(),
	)
}

// insertOperLog 用独立的超时写库；失败只记日志，不影响已经返回的响应。
func insertOperLog(repo repository.IOperLogRepository, entry *entity.OperLog) {
	ctx, cancel := context.WithTimeout(context.Background(), auditWriteTimeout)
	defer cancel()
	if err := repo.Insert(ctx, entry); err != nil {
		slog.Error("写审计日志失败",
			"err", err,
			"method", entry.RequestMethod,
			"url", entry.OperUrl,
			"operName", entry.OperName,
			"failedTotal", auditInsertFailures.Add(1),
		)
		return
	}
	auditInsertOK.Add(1)
	maybeLogAuditStats()
}

// auditRequired 判定一次请求是否需要落审计。方法先行：GET 一律不算写操作。
//
// 这里不能判断「当前有没有登录用户」：中间件挂在 JWTAuth 之前，入口处用户还没被写入
// context，判断会恒为 false。是否已登录由 buildOperLog 在 c.Next() 之后判定。
func auditRequired(c *gin.Context) bool {
	switch c.Request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return !isAuditSkipped(c.FullPath())
}

// auditResponseWriter 旁路缓存响应体，用来解析 {code,message} 判断业务成败。
// 只缓存前 limit 字节，其余透传；不改变任何响应行为。
type auditResponseWriter struct {
	gin.ResponseWriter
	buf   bytes.Buffer
	limit int
}

func (w *auditResponseWriter) Write(b []byte) (int, error) {
	w.capture(b)
	return w.ResponseWriter.Write(b)
}

func (w *auditResponseWriter) WriteString(s string) (int, error) {
	w.capture([]byte(s))
	return w.ResponseWriter.WriteString(s)
}

func (w *auditResponseWriter) capture(b []byte) {
	if w.buf.Len() >= w.limit {
		return
	}
	remain := w.limit - w.buf.Len()
	if len(b) > remain {
		b = b[:remain]
	}
	w.buf.Write(b)
}

func buildOperLog(c *gin.Context, w *auditResponseWriter, param string, meta *AuditMeta) *entity.OperLog {
	u := GetUser(c)
	if u == nil {
		return nil
	}
	now := types.Time(time.Now())
	operatorType := 1 // 后台用户
	if u.DeviceId != "" {
		operatorType = 2 // 桌面客户端设备
	}
	// 模块名优先取菜单标题（审计页展示的是"用户管理"，不是 system/user/edit）；
	// 未登记进菜单表的路由（参数路由、新接口）退回路由串，行为与没有缓存时一致。
	title := auditTitle(c.FullPath())
	if meta != nil {
		if t := meta.Title(c.FullPath()); t != "" {
			title = t
		}
	}
	deptName := ""
	if meta != nil {
		deptName = meta.DeptName(u.DeptId)
	}
	// OperId 不在这里生成：仓储 Insert 会用 idgen.New() 统一赋值，
	// 中间件再生成一份就是第二套 ID 规则，改一处漏一处。
	return &entity.OperLog{
		Title:         clip(title, colTitleLen),
		BusinessType:  businessType(c.Request.Method, c.Request.URL.Path),
		Method:        clip(c.HandlerName(), colMethodLen),
		RequestMethod: c.Request.Method,
		OperatorType:  operatorType,
		OperName:      clip(u.UserName, colOperNameLen),
		DeptName:      clip(deptName, colOperNameLen),
		OperUrl:       clip(c.Request.URL.Path, colOperURLLen),
		OperIp:        clip(c.ClientIP(), colOperIPLen),
		OperParam:     param,
		ErrorMsg:      clip(auditErrorMsg(w), colErrorMsgLen),
		OperTime:      &now,
	}
}

// businessType 按方法 + 路径判断业务类型。
//
// 只按方法猜是错的：exec / bind / command / enqueue 这些动作也是 POST，
// 一律记成"新增"会让审计里的类型列失真——而这一列是要被人当证据看的。
func businessType(method, path string) int {
	switch method {
	case http.MethodPost:
		if hasPathSuffix(path, "/add", "/create", "/save", "/import", "/upload") {
			return businessTypeAdd
		}
		return businessTypeOther
	case http.MethodPut, http.MethodPatch:
		return businessTypeEdit
	case http.MethodDelete:
		return businessTypeDelete
	default:
		return businessTypeOther
	}
}

func hasPathSuffix(path string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

// auditTitle 取路由模式串的前两段作为模块名，例如
// /api/v1/agent/instance/:id/exec → agent/instance。
// 不取全路径：带 :id 的路径又长又不可读，而审计页这一列叫「模块」，
// 本来就该是模块级粒度。菜单标题能命中时会覆盖它（见 buildOperLog）。
func auditTitle(fullPath string) string {
	p := strings.TrimPrefix(fullPath, "/api/v1")
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return ""
	}
	parts := strings.Split(p, "/")
	if len(parts) > 2 {
		parts = parts[:2]
	}
	return strings.Join(parts, "/")
}

// auditErrorMsg 业务失败时返回 message。本项目的失败走 HTTP 200 + code=-1，
// 只看状态码会漏掉全部业务失败，所以必须解析响应体。
//
// 响应体可能被 auditMaxRespBytes 截断（超长错误、流式输出），截断后 JSON 不完整，
// 直接 Unmarshal 会失败——那会把一次失败记成"成功"。所以解析失败时退回文本提取：
// 先取 code 判断成败，再抓 message 的字面量前缀，宁可截断也不丢"失败"这个事实。
func auditErrorMsg(w *auditResponseWriter) string {
	if w == nil {
		return ""
	}
	body := w.buf.Bytes()
	status := w.ResponseWriter.Status()
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &resp) == nil {
		if resp.Code != 0 {
			return resp.Message
		}
		if status >= http.StatusBadRequest {
			if resp.Message != "" {
				return resp.Message
			}
			return http.StatusText(status)
		}
		return ""
	}
	// 截断或非 JSON 响应
	if code, ok := auditPeekCode(body); ok && code != 0 {
		return auditPeekMessage(body)
	}
	if status >= http.StatusBadRequest {
		return http.StatusText(status)
	}
	return ""
}

var (
	auditCodeRe    = regexp.MustCompile(`"code"\s*:\s*(-?\d+)`)
	auditMessageRe = regexp.MustCompile(`"message"\s*:\s*"((?:[^"\\]|\\.)*)`)
)

func auditPeekCode(body []byte) (int, bool) {
	m := auditCodeRe.FindSubmatch(body)
	if m == nil {
		return 0, false
	}
	code, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return 0, false
	}
	return code, true
}

// auditPeekMessage 从被截断的 JSON 里取 message 的字面量。正则允许字符串未闭合，
// 所以截断在中间也能拿到前半段——总比把一次失败记成"无错误"强。
func auditPeekMessage(body []byte) string {
	m := auditMessageRe.FindSubmatch(body)
	if m == nil {
		return ""
	}
	literal := append(append([]byte{'"'}, m[1]...), '"')
	var msg string
	if json.Unmarshal(literal, &msg) == nil {
		return msg
	}
	return string(m[1])
}

// auditRequestParam 读取并还原请求体，返回掩码后的文本。
func auditRequestParam(c *gin.Context) string {
	if c.Request.Body == nil {
		return ""
	}
	contentType := c.GetHeader("Content-Type")
	if strings.Contains(contentType, "multipart/form-data") {
		return "[multipart/form-data omitted]"
	}
	// ContentLength < 0 是分块传输（长度未知）：此时 ReadAll 会把整个流读进内存，
	// 一个没有上限的上传就能把进程撑爆。宁可少记一个参数，也不赌请求体大小。
	if c.Request.ContentLength < 0 {
		return "[chunked body omitted]"
	}
	if c.Request.ContentLength > auditBodyReadLimit {
		return "[body too large: " + strconv.FormatInt(c.Request.ContentLength, 10) + " bytes]"
	}
	raw, err := io.ReadAll(c.Request.Body)
	// 无论成功还是中途出错都要还原：已经读走的字节必须放回，
	// 否则处理器拿到的是半截 body，报出来的错和真实原因无关。
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil {
		slog.Warn("读取请求体失败，已还原已读部分", "err", err, "path", c.FullPath())
		return "[body read interrupted]"
	}
	return truncateUTF8(maskSensitive(raw, contentType), auditMaxParamBytes)
}

// 敏感字段：命中即整值替换为 ***，避免密码、令牌、密钥进审计表。
var sensitiveKeyRe = regexp.MustCompile(`(?i)(password|passwd|pwd|token|secret|api[_-]?key|apikey|authorization|private[_-]?key|credential)`)

var formSensitiveRe = regexp.MustCompile(`(?i)((?:password|passwd|pwd|token|secret|api[_-]?key|apikey|authorization|private[_-]?key|credential)=)[^&]*`)

func maskSensitive(raw []byte, contentType string) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		var v interface{}
		if err := json.Unmarshal(trimmed, &v); err == nil {
			masked := maskValue(v)
			if b, err := json.Marshal(masked); err == nil {
				return string(b)
			}
		}
	}
	if strings.Contains(contentType, "application/x-www-form-urlencoded") || bytes.Contains(trimmed, []byte("=")) {
		return formSensitiveRe.ReplaceAllString(string(trimmed), "${1}***")
	}
	return string(trimmed)
}

func maskValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		for k, val := range t {
			if sensitiveKeyRe.MatchString(k) {
				t[k] = "***"
				continue
			}
			t[k] = maskValue(val)
		}
		return t
	case []interface{}:
		for i, item := range t {
			t[i] = maskValue(item)
		}
		return t
	default:
		return v
	}
}

// truncateUTF8 用于 text 列（oper_param）：按字节截，但必须退到字符边界。
// 直接 s[:max] 会把一个中文字切成两半，产生非法 UTF-8，PostgreSQL 会以
// "invalid byte sequence for encoding UTF8" 拒收整条记录——审计又静默消失了。
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && s[cut]&0xC0 == 0x80 { // 0x80-0xBF 是 UTF-8 续字节
		cut--
	}
	return s[:cut] + "…(truncated)"
}

// clip 按字符数裁剪到 varchar 列宽。PostgreSQL 的 varchar(n) 计的是字符不是字节，
// 所以必须按 rune 裁：按字节裁会让中文只剩三分之一，或者仍然超长导致 INSERT 失败。
func clip(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
