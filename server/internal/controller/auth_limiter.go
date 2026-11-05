package controller

import (
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// 登录/注册失败限流参数：同一 IP+用户名 在 15 分钟内失败 10 次后拒绝尝试。
// 内存窗口只在单实例内生效；多实例部署需要网关层再兜一层。
const (
	authFailWindow = 15 * time.Minute
	authFailMax    = 10 // 同一 IP+用户名
	authIPFailMax  = 50 // 同一 IP（换用户名也绕不过；同时兜住 bcrypt 放大）
	authFailMaxKey = 10000
	authPruneEvery = time.Minute // 饱和时最多每分钟清理一次，避免每次失败都全表扫描
)

const authIPKeyPrefix = "ip:"

type authFailure struct {
	count   int
	resetAt time.Time
}

// authFailureLimiter 登录/注册失败计数（固定窗口 + 成功即清零）。
type authFailureLimiter struct {
	mu        sync.Mutex
	entries   map[string]*authFailure
	lastPrune time.Time
	now       func() time.Time // 测试注入
}

func newAuthFailureLimiter() *authFailureLimiter {
	return &authFailureLimiter{entries: make(map[string]*authFailure), now: time.Now}
}

// Allow 报告 key 当前是否还能尝试。
func (l *authFailureLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.pruneIfSaturatedLocked(now)
	e, ok := l.entries[key]
	if !ok {
		return true
	}
	if !now.Before(e.resetAt) {
		delete(l.entries, key)
		return true
	}
	return e.count < l.maxForKey(key)
}

func (l *authFailureLimiter) maxForKey(key string) int {
	if strings.HasPrefix(key, authIPKeyPrefix) {
		return authIPFailMax
	}
	return authFailMax
}

// Fail 记一次失败；窗口过期则重新开窗。
func (l *authFailureLimiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if e, ok := l.entries[key]; ok {
		if now.Before(e.resetAt) {
			e.count++
			return
		}
		delete(l.entries, key)
	}
	l.pruneIfSaturatedLocked(now)
	// 有界兜底：清理后仍满，就随机丢一个旧条目给新 key 腾位置。
	// 攻击者仍可轮换 key 稀释限流效果，但内存与单次清理成本不会无限增长。
	if len(l.entries) >= authFailMaxKey {
		for k := range l.entries {
			delete(l.entries, k)
			break
		}
	}
	l.entries[key] = &authFailure{count: 1, resetAt: now.Add(authFailWindow)}
}

// Reset 登录/注册成功后清掉该 key 的失败计数。
func (l *authFailureLimiter) Reset(key string) {
	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

// pruneIfSaturatedLocked 只在 map 达到上限时清理过期项，且最多每分钟一次。
func (l *authFailureLimiter) pruneIfSaturatedLocked(now time.Time) {
	if len(l.entries) < authFailMaxKey || now.Sub(l.lastPrune) < authPruneEvery {
		return
	}
	for k, e := range l.entries {
		if !now.Before(e.resetAt) {
			delete(l.entries, k)
		}
	}
	l.lastPrune = now
}

// authKey 按 IP+用户名 计数。真实 IP 依赖 router.go 里的 SetTrustedProxies：
// 只信任回环反代，X-Forwarded-For 由 gin 从右往左取第一个非受信地址。
func authKey(c *gin.Context, username string) string {
	return c.ClientIP() + "|" + username
}

// authIPKey 纯 IP 维度，防止攻击者靠随机用户名让 userKey 永远命中新 key。
func authIPKey(c *gin.Context) string {
	return authIPKeyPrefix + c.ClientIP()
}
