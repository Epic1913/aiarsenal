package middleware

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"aiarsenal/domain/model"
)

const (
	// auditMetaTTL 缓存有效期。它只决定"改名多久后对新记录生效"，
	// 不是性能旋钮：审计记录的是动作发生时的名字，写进去就固化了，
	// 陈旧只影响新记录，不影响历史行。负载角度 1 分钟和 10 分钟都是噪音级。
	auditMetaTTL = 10 * time.Minute
	// auditMetaRetry 加载失败后的退避，避免每个写请求都去撞一次出问题的库。
	auditMetaRetry = 30 * time.Second
	// auditMetaWarmRetry 启动预热失败后的重试间隔。启动期 DB 往往还在迁移，
	// 用 30 秒会让头半分钟写入的行全都缺标题，所以单独给一个更短的退避。
	auditMetaWarmRetry = 5 * time.Second
	// auditMetaLoadTimeout 后台刷新的超时。它不占请求时间，可以放宽到 500ms。
	auditMetaLoadTimeout = 500 * time.Millisecond
)

var errAuditMetaWarm = errors.New("审计展示字段预热失败")

// menuTitleSource / deptNameSource 是为了测试而收窄的接口：
// 真实的 IMenuRepository / IDeptRepository 方法很多，审计只用到这两个读方法。
type menuTitleSource interface {
	GetMenuList(ctx context.Context) ([]*model.MenuInfoRes, error)
}

type deptNameSource interface {
	List(ctx context.Context, name, status string) ([]*model.DeptInfo, error)
}

// auditSnapshot 一次加载的完整快照。发布后只读，靠整体替换而不是加锁，
// 读者永远不会看到"菜单换了一半、部门还没换"的中间状态。
type auditSnapshot struct {
	titles map[string]string // 规范化后的路由 → 菜单标题
	depts  map[string]string // 部门 id → 部门名
}

// AuditMeta 缓存审计页要展示的两个名字：路由→菜单标题、部门 id→部门名。
//
// 设计目标是"写接口零等待"：
//   - 读路径只做一次 atomic load + 两次 map 查找，不碰锁、不碰库；
//   - 过期时由发现者踢一个后台 goroutine 去刷新，自己用旧快照继续返回；
//   - 同一时刻只允许一个刷新者（CAS），失败退避 30 秒，失败保留旧快照。
//
// 代价是"过期那一瞬间读到的是旧值"——对展示字段可以接受，对审计记录也无害：
// 写进库的是当时解析到的名字，本来就不应该随后续改名而变化。
type AuditMeta struct {
	menu menuTitleSource
	dept deptNameSource
	ttl  time.Duration

	snap       atomic.Pointer[auditSnapshot]
	loadedAt   atomic.Int64 // 最近一次成功加载（unixnano）
	attemptAt  atomic.Int64 // 最近一次尝试，成功或失败（unixnano）
	retryIn    atomic.Int64 // 当前失败退避（纳秒）：预热失败比运行期失败更短
	lastOK     atomic.Bool
	refreshing atomic.Bool
}

func NewAuditMeta(menu menuTitleSource, dept deptNameSource) *AuditMeta {
	m := &AuditMeta{menu: menu, dept: dept, ttl: auditMetaTTL}
	m.snap.Store(&auditSnapshot{titles: map[string]string{}, depts: map[string]string{}})
	m.retryIn.Store(int64(auditMetaRetry))
	return m
}

// Title 返回路由对应的菜单标题；未登记的路由（参数路由、未入菜单表的新接口）返回空串，
// 由调用方回退到路由串。
func (m *AuditMeta) Title(fullPath string) string {
	if m == nil {
		return ""
	}
	m.triggerRefresh()
	return m.snap.Load().titles[normalizeMenuPath(fullPath)]
}

// DeptName 返回部门名；未知部门返回空串。
func (m *AuditMeta) DeptName(deptID string) string {
	if m == nil || deptID == "" {
		return ""
	}
	m.triggerRefresh()
	return m.snap.Load().depts[deptID]
}

// Warm 在服务启动时同步加载一次，避免冷启动时前几条审计没有标题。
// 失败不阻断启动，返回错误由调用方决定要不要告警。
func (m *AuditMeta) Warm(ctx context.Context) error {
	if m == nil {
		return nil
	}
	titles, depts, ok := m.load(ctx)
	if !ok {
		// 启动期失败按短退避重试，避免头半分钟写入的行全都没有标题。
		m.retryIn.Store(int64(auditMetaWarmRetry))
	}
	m.publish(titles, depts, ok)
	if !ok {
		return errAuditMetaWarm
	}
	return nil
}

// triggerRefresh 只在过期且无人刷新时启动一个后台任务，调用方立即返回。
func (m *AuditMeta) triggerRefresh() {
	if !m.stale() {
		return
	}
	if !m.refreshing.CompareAndSwap(false, true) {
		return
	}
	go m.refresh()
}

func (m *AuditMeta) refresh() {
	defer m.refreshing.Store(false)
	// 后台 goroutine 里的 panic 会直接带走整个进程（没有 Recovery 兜）。
	// 展览字段刷新失败最多是标题旧一点，不该让 API 退出。
	defer func() {
		if r := recover(); r != nil {
			slog.Error("审计展示字段刷新 panic", "err", fmt.Sprint(r))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), auditMetaLoadTimeout)
	defer cancel()
	titles, depts, ok := m.load(ctx)
	m.publish(titles, depts, ok)
	if !ok {
		slog.Warn("审计展示字段刷新失败，继续用上次快照")
	}
}

func (m *AuditMeta) stale() bool {
	attempt := m.attemptAt.Load()
	if attempt == 0 {
		return true // 从未加载过
	}
	if time.Since(time.Unix(0, m.loadedAt.Load())) < m.ttl {
		return false // 最近一次成功且未过期
	}
	retry := time.Duration(m.retryIn.Load())
	if !m.lastOK.Load() && time.Since(time.Unix(0, attempt)) < retry {
		return false // 上次失败，退避期内不重试
	}
	return true
}

// publish 整体替换快照。本次没取到的部分沿用旧快照：
// 菜单查失败不该把部门也一起清空。
func (m *AuditMeta) publish(titles, depts map[string]string, ok bool) {
	next := &auditSnapshot{titles: titles, depts: depts}
	if old := m.snap.Load(); old != nil {
		if next.titles == nil {
			next.titles = old.titles
		}
		if next.depts == nil {
			next.depts = old.depts
		}
	}
	if next.titles == nil {
		next.titles = map[string]string{}
	}
	if next.depts == nil {
		next.depts = map[string]string{}
	}
	m.snap.Store(next)

	now := time.Now().UnixNano()
	m.attemptAt.Store(now)
	m.lastOK.Store(ok)
	if ok {
		m.loadedAt.Store(now)
		m.retryIn.Store(int64(auditMetaRetry)) // 成功后恢复常规退避
	}
}

func (m *AuditMeta) load(ctx context.Context) (titles map[string]string, depts map[string]string, ok bool) {
	ok = true
	if m.menu != nil {
		menus, err := m.menu.GetMenuList(ctx)
		if err != nil {
			slog.Warn("审计：加载菜单标题失败，沿用上次缓存", "err", err)
			ok = false
		} else {
			titles = make(map[string]string, len(menus))
			for _, menu := range menus {
				if menu == nil || menu.Title == "" || menu.Name == "" {
					continue
				}
				titles[normalizeMenuPath(menu.Name)] = menu.Title
			}
		}
	}
	if m.dept != nil {
		list, err := m.dept.List(ctx, "", "")
		if err != nil {
			slog.Warn("审计：加载部门名失败，沿用上次缓存", "err", err)
			ok = false
		} else {
			depts = make(map[string]string, len(list))
			for _, d := range list {
				if d == nil || d.DeptId == "" {
					continue
				}
				depts[d.DeptId] = d.DeptName
			}
		}
	}
	return titles, depts, ok
}

// normalizeMenuPath 对齐两种写法：请求侧是 "/api/v1/x"，菜单表里既有
// "api/v1/x" 也有 "/api/v1/x"，个别还带查询串。
func normalizeMenuPath(p string) string {
	p = strings.TrimPrefix(p, "/")
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	return p
}
