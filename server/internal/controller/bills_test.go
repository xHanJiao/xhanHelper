package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"xhan-helper/internal/model"
	"xhan-helper/internal/service"
)

// CSV 导入先校验、末尾批量插入：合法行全进，非法行只进 errors，且不再逐行走库。
func TestImportBillsBatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "bills.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Bill{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	csv := "项目,实际消费,一级分类,二级分类,消费类别,日期,消费类型,人员,备注\n" +
		"早餐,12.5,餐饮,堂食,一次性,2026/11/01,微信,我,\n" +
		"地铁,4,交通,地铁,一次性,2026-11-01,支付宝,我,通勤\n" +
		"坏行,abc,餐饮,堂食,一次性,2026-11-01,微信,我,\n"

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/bills/import", strings.NewReader(csv))
	c.Set("user_id", "u1")
	NewBillHandler(service.NewBillRepo(db)).ImportBills(c)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Imported int      `json:"imported"`
			Skipped  int      `json:"skipped"`
			Total    int      `json:"total"`
			Errors   []string `json:"errors"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.Imported != 2 || resp.Data.Skipped != 1 || resp.Data.Total != 3 || len(resp.Data.Errors) != 1 {
		t.Fatalf("unexpected response: %+v", resp.Data)
	}

	var count int64
	if err := db.Model(&model.Bill{}).Where("user_id = ?", "u1").Count(&count).Error; err != nil {
		t.Fatalf("count bills: %v", err)
	}
	if count != 2 {
		t.Fatalf("want 2 rows, got %d", count)
	}
}
