package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"xhan-helper/internal/model"
)

func TestNormalizeRowFixesInvalidUTF8(t *testing.T) {
	type row struct{ Name string }
	r := row{Name: "abc\xe5"} // 源里被按字节截断的尾部
	stats := &utf8Stats{}
	normalizeRow(reflect.ValueOf(&r).Elem(), stats)
	if !utf8.ValidString(r.Name) {
		t.Fatalf("清洗后仍非法: %q", r.Name)
	}
	if r.Name != "abc\uFFFD" {
		t.Fatalf("got %q, want %q", r.Name, "abc\uFFFD")
	}
	if stats.invalidUTF8 != 1 {
		t.Fatalf("count=%d, want 1", stats.invalidUTF8)
	}
}

func TestDedupConceptEmbeddingsKeepsNewest(t *testing.T) {
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := old.Add(time.Hour)
	rows := []model.ConceptEmbedding{
		{ID: "a", Type: "topic", Value: "Agent开发", UserID: "u1", CreatedAt: old},
		{ID: "b", Type: "topic", Value: "agent开发", UserID: "u1", CreatedAt: newer}, // 小写归一后与 a 撞键
		{ID: "c", Type: "topic", Value: "其他", UserID: "u1", CreatedAt: old},
		{ID: "d", Type: "topic", Value: "Agent开发", UserID: "u2", CreatedAt: old}, // 不同用户，保留
	}
	kept, dropped := dedupConceptEmbeddings(rows)
	if dropped != 1 {
		t.Fatalf("dropped=%d, want 1", dropped)
	}
	if len(kept) != 3 {
		t.Fatalf("kept=%d, want 3", len(kept))
	}
	for _, r := range kept {
		if r.ID == "a" {
			t.Fatalf("旧行 a 应被丢弃，保留了较新者")
		}
	}
	// 结果按 ID 升序：b、c、d
	if kept[0].ID != "b" || kept[1].ID != "c" || kept[2].ID != "d" {
		t.Fatalf("结果未按 ID 升序: %v %v %v", kept[0].ID, kept[1].ID, kept[2].ID)
	}
}

func TestNormalizeDate(t *testing.T) {
	cases := map[string]string{
		"2026-08-12T00:00:00Z": "2026-08-12",
		"2026-10-01":           "2026-10-01",
		"":                     "",
		"2026-1":               "2026-1", // 不足 10 位原样（插入时若非法会显式报错）
	}
	for in, want := range cases {
		if got := normalizeDate(in); got != want {
			t.Errorf("normalizeDate(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestParseSQLiteTime(t *testing.T) {
	layouts := []string{
		"2026-09-30 15:35:50.611173+08:00",
		"2026-09-30T15:35:50.611173+08:00",
		"2026-09-30T15:35:50.611173Z",
		"2026-09-30 15:35:50+08:00",
	}
	for _, s := range layouts {
		if _, err := parseSQLiteTime(s); err != nil {
			t.Errorf("parseSQLiteTime(%q) 失败: %v", s, err)
		}
	}
	if _, err := parseSQLiteTime("not-a-time"); err == nil {
		t.Errorf("非法时间应报错")
	}
}

func TestEstimateRowBytes(t *testing.T) {
	type row struct {
		A string
		B string
		C int
	}
	got := estimateRowBytes(row{A: "12345", B: "67", C: 999})
	if got < 7 {
		t.Fatalf("estimateRowBytes=%d, 至少应含两个字符串长度 7", got)
	}
}

// 大字段是指针（*string/*[]byte）时必须计入，否则字节预算失效、整表挤一条 INSERT。
func TestEstimateRowBytesCountsPointers(t *testing.T) {
	big := strings.Repeat("x", 500000)
	blob := make([]byte, 1000)
	var nilStr *string
	type row struct {
		Direct *string
		Bytes  *[]byte
		Small  *string // nil
		Plain  string
	}
	got := estimateRowBytes(row{Direct: &big, Bytes: &blob, Small: nilStr, Plain: "abc"})
	if got < 500000+1000+3 {
		t.Fatalf("estimateRowBytes=%d，未计入 *string/*[]byte 大字段", got)
	}

	// 真实模型：Embedding.Embedding 是指针，估算应覆盖其长度
	e := strings.Repeat("1,", 20000) // ~40KB
	emb := model.Embedding{Embedding: &e}
	if got := estimateRowBytes(emb); got < len(e) {
		t.Fatalf("estimateRowBytes(Embedding)=%d < %d，漏算 *string", got, len(e))
	}
}

func TestNormalizeRowFixesInvalidUTF8Pointer(t *testing.T) {
	bad := "ok\xe5" // 尾部被截断
	type row struct{ Name *string }
	r := row{Name: &bad}
	stats := &utf8Stats{}
	normalizeRow(reflect.ValueOf(&r).Elem(), stats)
	if !utf8.ValidString(*r.Name) {
		t.Fatalf("清洗后仍非法: %q", *r.Name)
	}
	if *r.Name != "ok\uFFFD" {
		t.Fatalf("got %q, want %q", *r.Name, "ok\uFFFD")
	}
	if stats.invalidUTF8 != 1 {
		t.Fatalf("count=%d, want 1", stats.invalidUTF8)
	}
}
