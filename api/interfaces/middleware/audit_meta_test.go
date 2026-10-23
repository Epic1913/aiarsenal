package middleware

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"aiarsenal/domain/model"
)

type fakeMenuSource struct {
	calls int
	menus []*model.MenuInfoRes
	err   error
}

func (f *fakeMenuSource) GetMenuList(context.Context) ([]*model.MenuInfoRes, error) {
	f.calls++
	return f.menus, f.err
}

type fakeDeptSource struct {
	calls int
	list  []*model.DeptInfo
	err   error
}

func (f *fakeDeptSource) List(context.Context, string, string) ([]*model.DeptInfo, error) {
	f.calls++
	return f.list, f.err
}

// waitAuditMetaIdle 等后台刷新结束，避免用例在 goroutine 还没跑完时断言。
func waitAuditMetaIdle(t *testing.T, m *AuditMeta) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !m.refreshing.Load() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("后台刷新未在期限内结束")
}

func TestAuditMetaWarmThenServe(t *testing.T) {
	menu := &fakeMenuSource{menus: []*model.MenuInfoRes{
		{Name: "api/v1/system/user/edit", Title: "用户管理"},
	}}
	dept := &fakeDeptSource{list: []*model.DeptInfo{{DeptId: "d1", DeptName: "研发部"}}}
	meta := NewAuditMeta(menu, dept)

	if err := meta.Warm(context.Background()); err != nil {
		t.Fatalf("预热失败：%v", err)
	}
	if got := meta.Title("/api/v1/system/user/edit"); got != "用户管理" {
		t.Fatalf("Title = %q", got)
	}
	if got := meta.DeptName("d1"); got != "研发部" {
		t.Fatalf("DeptName = %q", got)
	}
	// 预热之后再读不该再查库
	meta.Title("/api/v1/system/user/edit")
	if menu.calls != 1 || dept.calls != 1 {
		t.Fatalf("预热后又查库：menu=%d dept=%d", menu.calls, dept.calls)
	}
}

func TestAuditMetaNormalizesMenuPath(t *testing.T) {
	menu := &fakeMenuSource{menus: []*model.MenuInfoRes{
		{Name: "/api/v1/system/role/edit", Title: "角色管理"},      // 菜单表里有带前导斜杠的
		{Name: "api/v1/system/menuList?type=1", Title: "菜单管理"}, // 也有带查询串的
	}}
	meta := NewAuditMeta(menu, nil)
	if err := meta.Warm(context.Background()); err != nil {
		t.Fatalf("预热失败：%v", err)
	}

	if got := meta.Title("/api/v1/system/role/edit"); got != "角色管理" {
		t.Errorf("带前导斜杠的菜单未命中：%q", got)
	}
	if got := meta.Title("/api/v1/system/menuList"); got != "菜单管理" {
		t.Errorf("带查询串的菜单未命中：%q", got)
	}
}

func TestAuditMetaUnknownRouteReturnsEmpty(t *testing.T) {
	meta := NewAuditMeta(&fakeMenuSource{}, nil)
	if err := meta.Warm(context.Background()); err != nil {
		t.Fatalf("预热失败：%v", err)
	}
	if got := meta.Title("/api/v1/agent/instance/:id/exec"); got != "" {
		t.Fatalf("未登记路由应返回空串（由调用方回退到路径），得到 %q", got)
	}
}

func TestAuditMetaWarmReportsFailure(t *testing.T) {
	meta := NewAuditMeta(&fakeMenuSource{err: errors.New("db down")}, nil)
	if err := meta.Warm(context.Background()); err == nil {
		t.Fatal("预热失败应返回错误，交给调用方记日志")
	}
}

// 失败后退避：不能每个写请求都去撞一次正在出问题的库。
func TestAuditMetaBacksOffAfterFailure(t *testing.T) {
	menu := &fakeMenuSource{err: errors.New("db down")}
	meta := NewAuditMeta(menu, nil)

	if err := meta.Warm(context.Background()); err == nil {
		t.Fatal("预热应失败")
	}
	meta.Title("/api/v1/a")
	meta.Title("/api/v1/a")

	if menu.calls != 1 {
		t.Fatalf("失败后仍反复查询：%d 次", menu.calls)
	}
}

// 刷新失败必须保留上次成功的快照。
func TestAuditMetaKeepsOldSnapshotOnRefreshError(t *testing.T) {
	menu := &fakeMenuSource{menus: []*model.MenuInfoRes{{Name: "api/v1/a", Title: "旧标题"}}}
	dept := &fakeDeptSource{list: []*model.DeptInfo{{DeptId: "d1", DeptName: "研发部"}}}
	meta := NewAuditMeta(menu, dept)
	if err := meta.Warm(context.Background()); err != nil {
		t.Fatalf("预热失败：%v", err)
	}

	menu.err = errors.New("db down")
	menu.menus = nil
	dept.err = errors.New("db down")
	dept.list = nil
	meta.ttl = -time.Second // 强制下一次读取触发刷新
	if err := meta.Warm(context.Background()); err == nil {
		t.Fatal("第二次预热应失败")
	}

	if got := meta.Title("/api/v1/a"); got != "旧标题" {
		t.Fatalf("刷新失败后丢了旧标题：%q", got)
	}
	if got := meta.DeptName("d1"); got != "研发部" {
		t.Fatalf("菜单/部门同时失败时丢了旧部门：%q", got)
	}
}

// blockingMenuSource 卡住直到测试放行，用来证明"读路径没有等查询"，
// 不依赖毫秒级计时（CI runner 上会 flake）。
type blockingMenuSource struct {
	started chan struct{}
	release chan struct{}
	menus   []*model.MenuInfoRes
	once    sync.Once
}

func (s *blockingMenuSource) GetMenuList(ctx context.Context) ([]*model.MenuInfoRes, error) {
	s.once.Do(func() { close(s.started) })
	select {
	case <-s.release:
		return s.menus, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// 读路径不能等刷新：查询还卡着的时候，Title 也必须立刻返回。
func TestAuditMetaReadDoesNotWaitForRefresh(t *testing.T) {
	menu := &blockingMenuSource{
		started: make(chan struct{}),
		release: make(chan struct{}),
		menus:   []*model.MenuInfoRes{{Name: "api/v1/a", Title: "A"}},
	}
	meta := NewAuditMeta(menu, nil)

	if got := meta.Title("/api/v1/a"); got != "" {
		t.Fatalf("冷启动首次读取应为空（后台还在加载），得到 %q", got)
	}
	<-menu.started // 后台确实开始查了，而且此时仍被卡住
	if got := meta.Title("/api/v1/a"); got != "" {
		t.Fatalf("刷新未完成时不该有值，得到 %q", got)
	}

	close(menu.release)
	waitAuditMetaIdle(t, meta)
	if got := meta.Title("/api/v1/a"); got != "A" {
		t.Fatalf("后台刷新后仍未生效：%q", got)
	}
}

// 端到端：中间件落库时写的是菜单标题和部门名，不是路由串和空串。
func TestOperLogUsesMenuTitleAndDeptName(t *testing.T) {
	repo := &fakeOperLogRepo{}
	meta := NewAuditMeta(
		&fakeMenuSource{menus: []*model.MenuInfoRes{{Name: "api/v1/system/user/edit", Title: "用户管理"}}},
		&fakeDeptSource{list: []*model.DeptInfo{{DeptId: "d1", DeptName: "研发部"}}},
	)
	if err := meta.Warm(context.Background()); err != nil {
		t.Fatalf("预热失败：%v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	v1 := r.Group("/api/v1")
	v1.Use(OperLog(repo, meta), func(c *gin.Context) {
		SetUser(c, &model.LoginUserRes{Id: "u1", UserName: "alice", DeptId: "d1"})
	})
	v1.POST("/system/user/edit", func(c *gin.Context) { Success(c, nil) })

	doPost(t, r, "/api/v1/system/user/edit", `{}`)

	if len(repo.inserted) != 1 {
		t.Fatalf("落库条数 = %d", len(repo.inserted))
	}
	if got := repo.inserted[0].Title; got != "用户管理" {
		t.Errorf("Title = %q，期望菜单标题", got)
	}
	if got := repo.inserted[0].DeptName; got != "研发部" {
		t.Errorf("DeptName = %q", got)
	}
}

type panicMenuSource struct{}

func (panicMenuSource) GetMenuList(context.Context) ([]*model.MenuInfoRes, error) {
	panic("boom")
}

// 后台刷新 panic 不能带走进程：一个展示字段的故障不该让 API 退出。
func TestAuditMetaRefreshPanicIsContained(t *testing.T) {
	meta := NewAuditMeta(panicMenuSource{}, nil)

	if got := meta.Title("/api/v1/a"); got != "" {
		t.Fatalf("Title = %q", got)
	}
	waitAuditMetaIdle(t, meta)
	if got := meta.Title("/api/v1/a"); got != "" {
		t.Fatalf("panic 后不该有值：%q", got)
	}
}
