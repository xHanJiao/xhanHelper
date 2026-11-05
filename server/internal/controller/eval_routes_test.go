package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
)

// 项目名会拼进文件路径（<evalDir>/<project>/eval.db），必须挡住 ../ 等穿越写法。
// 走真路由 + 分组中间件：同时锁住「拒绝时下游 handler 不执行」——
// 守卫生效依赖 view.JSONError 里的 c.Abort()，直接调中间件测不出这一点。
func TestValidateEvalProject(t *testing.T) {
	gin.SetMode(gin.TestMode)

	run := func(project string) (int, bool) {
		called := false
		r := gin.New()
		g := r.Group("/api/admin/eval", validateEvalProject)
		g.GET("/projects", func(c *gin.Context) { called = true })
		g.GET("/projects/:project/stats", func(c *gin.Context) { called = true })

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
			"/api/admin/eval/projects/"+url.PathEscape(project)+"/stats", nil))
		return w.Code, called
	}

	for _, ok := range []string{"retrieval-v1", "proj_2", "A1"} {
		if code, called := run(ok); code != http.StatusOK || !called {
			t.Errorf("project %q 应放行并执行下游, got status=%d called=%v", ok, code, called)
		}
	}

	// 无 :project 的列表路由不应被守卫拦住
	r := gin.New()
	called := false
	g := r.Group("/api/admin/eval", validateEvalProject)
	g.GET("/projects", func(c *gin.Context) { called = true })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/eval/projects", nil))
	if w.Code != http.StatusOK || !called {
		t.Errorf("/projects 应放行, got status=%d called=%v", w.Code, called)
	}

	for _, bad := range []string{"..", ".", "%2e%2e", "a b", "a.db", "中文"} {
		code, called := run(bad)
		if code != http.StatusBadRequest {
			t.Errorf("project %q 应返回 400, got %d", bad, code)
		}
		if called {
			t.Errorf("project %q 被拒后下游 handler 不应执行", bad)
		}
	}

	// 带 / 的写法在路由层就匹配不上 :project（不会进 handler），404 即可
	if code, called := run("a/b"); code != http.StatusNotFound || called {
		t.Errorf("project %q 应 404 且不进 handler, got status=%d called=%v", "a/b", code, called)
	}
}
