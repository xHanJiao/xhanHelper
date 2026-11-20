package controller

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/idgen"
	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

func setupProfileHandler(t *testing.T) (*ProfileHandler, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "p.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.UserProfileFact{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := idgen.Init(1); err != nil {
		t.Fatalf("init idgen: %v", err)
	}
	return NewProfileHandler(service.NewUserProfileFactRepo(db)), db
}

// 空字段也必须回 []（Swift 侧 [Item] 解码不接受 null），且按层分组、身份层在最前。
func TestProfileHandler_GetProfile_GroupsAndEmptyItems(t *testing.T) {
	h, db := setupProfileHandler(t)
	if err := db.Create(&model.UserProfileFact{
		ID: "f1", UserID: "u1", Field: "称呼", Value: "哥",
		Status: model.FactStatusActive, BatchID: "b1",
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	r := gin.New()
	r.GET("/api/profile", func(c *gin.Context) {
		c.Set("user_id", "u1")
		h.GetProfile(c)
	})
	w := doJSON(r, "GET", "/api/profile", "")
	if w.Code != 200 {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Status string `json:"status"`
		Data   struct {
			Groups []profileLayerDTO `json:"groups"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v, body=%s", err, w.Body.String())
	}
	if len(resp.Data.Groups) == 0 || resp.Data.Groups[0].Layer != 0 {
		t.Fatalf("identity layer should come first, got %+v", resp.Data.Groups)
	}
	var names []string
	for _, f := range resp.Data.Groups[0].Fields {
		names = append(names, f.Field)
	}
	for _, want := range []string{"称呼", "性别", "出生日期", "籍贯", "常驻地"} {
		if !strings.Contains(strings.Join(names, ","), want) {
			t.Errorf("identity layer should contain %s, got %v", want, names)
		}
	}
	// 空字段回 []，不是 null
	if !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Errorf("empty fields must serialize items as []: %s", w.Body.String())
	}
}

func TestProfileHandler_CreateUpdateDelete(t *testing.T) {
	h, _ := setupProfileHandler(t)
	r := gin.New()
	r.POST("/api/profile/facts", func(c *gin.Context) { c.Set("user_id", "u1"); h.CreateFact(c) })
	r.PATCH("/api/profile/facts/:id", func(c *gin.Context) { c.Set("user_id", "u1"); h.UpdateFact(c) })
	r.DELETE("/api/profile/facts/:id", func(c *gin.Context) { c.Set("user_id", "u1"); h.DeleteFact(c) })

	// 非白名单字段 → 400
	if w := doJSON(r, "POST", "/api/profile/facts", `{"field":"补充说明","value":"x"}`); w.Code != 400 {
		t.Errorf("non-whitelist field should be 400, got %d", w.Code)
	}
	// 超长 → 400
	long := ""
	for i := 0; i < 11; i++ {
		long += "字"
	}
	if w := doJSON(r, "POST", "/api/profile/facts", `{"field":"称呼","value":"`+long+`"}`); w.Code != 400 {
		t.Errorf("over-long value should be 400, got %d", w.Code)
	}

	w := doJSON(r, "POST", "/api/profile/facts", `{"field":"称呼","value":"哥"}`)
	if w.Code != 200 {
		t.Fatalf("create status = %d, body=%s", w.Code, w.Body.String())
	}
	var created struct {
		Data profileItemDTO `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Data.Source != model.FactSourceUser {
		t.Errorf("user-created fact source = %s, want user", created.Data.Source)
	}

	if w := doJSON(r, "PATCH", "/api/profile/facts/"+created.Data.ID, `{"value":"哥本哈根"}`); w.Code != 200 {
		t.Errorf("patch status = %d, body=%s", w.Code, w.Body.String())
	}
	// 改超长 → 400（与新增路径同一套语义，不能静默截断）
	overlong := ""
	for i := 0; i < 11; i++ {
		overlong += "字"
	}
	if w := doJSON(r, "PATCH", "/api/profile/facts/"+created.Data.ID, `{"value":"`+overlong+`"}`); w.Code != 400 {
		t.Errorf("over-long patch should be 400, got %d", w.Code)
	}
	// 别人的 id → 404
	if w := doJSON(r, "DELETE", "/api/profile/facts/not-mine", ""); w.Code != 404 {
		t.Errorf("unknown id should be 404, got %d", w.Code)
	}
	if w := doJSON(r, "DELETE", "/api/profile/facts/"+created.Data.ID, ""); w.Code != 200 {
		t.Errorf("delete status = %d, body=%s", w.Code, w.Body.String())
	}
}

func doJSON(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
