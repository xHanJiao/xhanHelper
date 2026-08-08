package profilefield

import (
	"strings"
	"testing"
)

func TestProfileFields_Whitelist(t *testing.T) {
	for _, f := range ProfileFields {
		if !InProfileWhitelist(f.Field) {
			t.Errorf("field %q should be in whitelist", f.Field)
		}
		if strings.TrimSpace(f.ExtractDesc) == "" {
			t.Errorf("field %q ExtractDesc is empty", f.Field)
		}
		if strings.TrimSpace(f.InjectDesc) == "" {
			t.Errorf("field %q InjectDesc is empty", f.Field)
		}
	}
	if InProfileWhitelist("不存在字段") {
		t.Error("non-whitelist field should be rejected")
	}
	if ProfileFieldDesc("不存在字段") != nil {
		t.Error("ProfileFieldDesc for non-whitelist field should be nil")
	}
}

func TestProfileFields_IncludesAllDocumentedFields(t *testing.T) {
	want := []string{"称呼", "出生年份", "籍贯", "性别", "职业领域", "重要关系", "饮食习惯", "财务概况", "长期目标", "补充说明"}
	for _, w := range want {
		if !InProfileWhitelist(w) {
			t.Errorf("documented field %q missing from whitelist", w)
		}
	}
}

func TestProfileExtractFieldText_ContainsAllFields(t *testing.T) {
	text := ProfileExtractFieldText()
	for _, f := range ProfileFields {
		if !strings.Contains(text, f.Field) {
			t.Errorf("field %q missing from extraction text", f.Field)
		}
	}
}

func TestProfileFieldNames(t *testing.T) {
	names := ProfileFieldNames()
	if len(names) != len(ProfileFields) {
		t.Fatalf("expected %d names, got %d", len(ProfileFields), len(names))
	}
	for i, n := range names {
		if n != ProfileFields[i].Field {
			t.Errorf("names[%d]=%q, expected %q", i, n, ProfileFields[i].Field)
		}
	}
}
