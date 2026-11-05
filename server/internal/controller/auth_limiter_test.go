package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestAuthFailureLimiter(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := newAuthFailureLimiter()
	l.now = func() time.Time { return now }
	key := "192.0.2.1|alice"

	for i := 0; i < authFailMax; i++ {
		if !l.Allow(key) {
			t.Fatalf("第 %d 次尝试应放行", i+1)
		}
		l.Fail(key)
	}
	if l.Allow(key) {
		t.Fatalf("失败 %d 次后应拒绝", authFailMax)
	}

	// 成功登录清零
	l.Reset(key)
	if !l.Allow(key) {
		t.Fatal("Reset 后应放行")
	}

	// 窗口过期自动放行
	l.Fail(key)
	now = now.Add(authFailWindow)
	if !l.Allow(key) {
		t.Fatal("窗口过期后应放行")
	}

	// 不同 key 互不影响
	l.Fail("192.0.2.2|bob")
	if !l.Allow("192.0.2.3|carol") {
		t.Fatal("未失败过的 key 不应被拦截")
	}
}

// 换用户名不能绕过：同一 IP 有独立的高阈值上限，兜住 bcrypt 成本放大。
func TestAuthFailureLimiterIPCap(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := newAuthFailureLimiter()
	l.now = func() time.Time { return now }

	for i := 0; i < authIPFailMax; i++ {
		l.Fail("ip:192.0.2.1")
	}
	if l.Allow("ip:192.0.2.1") {
		t.Fatal("同一 IP 失败超限后应拒绝")
	}
	if !l.Allow("192.0.2.1|alice") {
		t.Fatal("IP 限流不应连带拦截具体的用户名 key")
	}
}

// 只信任回环反代：客户端伪造的左侧 XFF 不能成为限流 key。
func TestTrustedProxyClientIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if err := r.SetTrustedProxies(trustedProxyCIDRs); err != nil {
		t.Fatalf("set trusted proxies: %v", err)
	}
	var got string
	r.GET("/ip", func(c *gin.Context) { got = c.ClientIP() })

	req := httptest.NewRequest(http.MethodGet, "/ip", nil)
	req.RemoteAddr = "127.0.0.1:12345" // nginx 回环
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.7")
	r.ServeHTTP(httptest.NewRecorder(), req)

	if got != "203.0.113.7" {
		t.Fatalf("应取右侧真实客户端地址，got %q", got)
	}
}

// 攻击者可随意更换 key：map 必须有硬上限，不能随请求数线性增长。
func TestAuthFailureLimiterMemoryBounded(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := newAuthFailureLimiter()
	l.now = func() time.Time { return now }

	for i := 0; i < authFailMaxKey+500; i++ {
		l.Fail(fmt.Sprintf("192.0.2.1|u%d", i))
	}
	if got := len(l.entries); got > authFailMaxKey {
		t.Fatalf("entries 超上限: %d > %d", got, authFailMaxKey)
	}

	// 饱和状态下窗口过期：清理后旧条目应被清空，只留新 key。
	now = now.Add(authFailWindow + time.Minute)
	l.Fail("192.0.2.9|new")
	if got := len(l.entries); got != 1 {
		t.Fatalf("过期条目应被清理，got %d", got)
	}
}

// 连续失败到达阈值后，登录入口直接 429，且不会触到 repo（传 nil 也安全）。
func TestLoginRejectsAfterTooManyFailures(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Unix(1_700_000_000, 0)
	limiter := newAuthFailureLimiter()
	limiter.now = func() time.Time { return now }

	h := loginUser(nil, nil, limiter)
	do := func() int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/auth/login",
			strings.NewReader(`{"username":"alice","password":"bad"}`))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.RemoteAddr = "192.0.2.1:12345"
		h(c)
		return w.Code
	}

	for i := 0; i < authFailMax; i++ {
		limiter.Fail("192.0.2.1|alice")
	}
	if code := do(); code != http.StatusTooManyRequests {
		t.Fatalf("失败超限后应 429, got %d", code)
	}
}

// 随机换用户名时 userKey 永远放行，必须由 IP 维度在进入 repo 前挡住。
func TestLoginRejectsAfterIPFailures(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Unix(1_700_000_000, 0)
	limiter := newAuthFailureLimiter()
	limiter.now = func() time.Time { return now }
	for i := 0; i < authIPFailMax; i++ {
		limiter.Fail("ip:192.0.2.1")
	}

	h := loginUser(nil, nil, limiter)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"never-tried-before","password":"whatever"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.RemoteAddr = "192.0.2.1:12345"
	h(c)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("IP 失败超限后应 429, got %d", w.Code)
	}
}
