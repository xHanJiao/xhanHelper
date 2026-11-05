package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// 项目名会拼进文件路径，必须挡住 ../ 等穿越写法。
func TestValidateEvalProject(t *testing.T) {
	gin.SetMode(gin.TestMode)

	run := func(project string) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/admin/eval/projects/x/stats", nil)
		if project != "" {
			c.Params = gin.Params{{Key: "project", Value: project}}
		}
		validateEvalProject(c)
		if w.Code == 0 {
			return http.StatusOK // 未写响应 = 放行
		}
		return w.Code
	}

	for _, ok := range []string{"", "retrieval-v1", "proj_2", "A1"} {
		if code := run(ok); code != http.StatusOK {
			t.Errorf("project %q 应放行, got %d", ok, code)
		}
	}
	for _, bad := range []string{"..", "../../etc", "a/b", "a b", "a.db", "中文"} {
		if code := run(bad); code != http.StatusBadRequest {
			t.Errorf("project %q 应拒绝, got %d", bad, code)
		}
	}
}
