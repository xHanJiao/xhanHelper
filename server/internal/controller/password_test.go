package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		password string
		ok       bool
	}{
		{"", false},
		{"123456789", false}, // 9 个字符
		{"1234567890", true}, // 10 个字符
		{strings.Repeat("a", 72), true},
		{strings.Repeat("a", 73), false}, // bcrypt 只吃 72 字节
		{strings.Repeat("密", 10), true},  // 10 个字符（30 字节）
	}
	for _, tc := range cases {
		err := validatePassword(tc.password)
		if tc.ok && err != nil {
			t.Errorf("password len=%d 应通过: %v", len(tc.password), err)
		}
		if !tc.ok && err == nil {
			t.Errorf("password len=%d 应拒绝", len(tc.password))
		}
	}
}

func TestRandomPassword(t *testing.T) {
	a, err := randomPassword()
	if err != nil {
		t.Fatal(err)
	}
	b, err := randomPassword()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("两次生成的密码不应相同")
	}
	if err := validatePassword(a); err != nil {
		t.Fatalf("随机密码应满足长度要求: %v", err)
	}
}

// 管理员创建用户也要走同一套密码校验；校验在 repo 之前，传 nil repo 也安全。
func TestCreateUserRejectsShortPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/admin/users",
		strings.NewReader(`{"username":"alice","password":"123456789"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	createUserByAdmin(nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("短密码应 400, got %d body=%s", w.Code, w.Body.String())
	}
}
