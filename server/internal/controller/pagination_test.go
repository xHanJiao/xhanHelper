package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestParseListLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	run := func(query string) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/x"+query, nil)
		return parseListLimit(c, 50)
	}

	cases := []struct {
		query string
		want  int
	}{
		{"", 50},
		{"?limit=20", 20},
		{"?limit=0", 50},
		{"?limit=-5", 50},
		{"?limit=abc", 50},
		{"?limit=200", 200},
		{"?limit=100000", listLimitMax},
	}
	for _, tc := range cases {
		if got := run(tc.query); got != tc.want {
			t.Errorf("%q: want %d, got %d", tc.query, tc.want, got)
		}
	}
}

func TestParseListLimitAllowZero(t *testing.T) {
	gin.SetMode(gin.TestMode)
	run := func(query string) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/x"+query, nil)
		return parseListLimitAllowZero(c, 20)
	}
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"", 20},
		{"?limit=0", 0}, // 只取 total 的语义必须保留
		{"?limit=-1", 20},
		{"?limit=abc", 20},
		{"?limit=100000", listLimitMax},
	} {
		if got := run(tc.query); got != tc.want {
			t.Errorf("%q: want %d, got %d", tc.query, tc.want, got)
		}
	}
}
