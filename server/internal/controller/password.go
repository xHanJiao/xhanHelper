package controller

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"unicode/utf8"
)

const (
	passwordMinChars = 10
	passwordMaxBytes = 72 // bcrypt 只使用前 72 字节，超出必须拒绝而不是静默截断
)

func validatePassword(password string) error {
	if utf8.RuneCountInString(password) < passwordMinChars {
		return fmt.Errorf("password must be at least %d characters", passwordMinChars)
	}
	if len(password) > passwordMaxBytes {
		return fmt.Errorf("password must be at most %d bytes", passwordMaxBytes)
	}
	return nil
}

// randomPassword 生成管理员重置用的随机密码（crypto/rand，16 字节 → 22 个 base64url 字符）。
func randomPassword() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
