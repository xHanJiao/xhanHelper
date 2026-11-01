package agent

import (
	"encoding/json"
	"testing"

	"xhan-helper/internal/model"
	"xhan-helper/internal/tool"
)

// 只有「写页面」（create / patch）才挂卡：get 的 outline / block / full 也带
// artifact_id，但读一下不该在聊天里多一张卡。
func TestArtifactRefsFromResultsOnlyCreateAndPatch(t *testing.T) {
	data, err := json.Marshal(map[string]string{
		"artifact_id": "a1", "type": "html", "title": "10.18 手账", "summary": "一周记录",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	results := []toolExecResult{
		{tc: tool.ToolCall{Tool: "html", Function: "create"}, result: &tool.Result{Success: true, Data: data}},
		{tc: tool.ToolCall{Tool: "html", Function: "get"}, result: &tool.Result{Success: true, Data: data}},
		{tc: tool.ToolCall{Tool: "html", Function: "patch"}, result: &tool.Result{Success: true, Data: data}},
		{tc: tool.ToolCall{Tool: "document", Function: "create"}, result: &tool.Result{Success: true, Data: data}},
		{tc: tool.ToolCall{Tool: "html", Function: "create"}, result: &tool.Result{Success: false}},
	}
	refs := artifactRefsFromResults(results)
	if len(refs) != 2 {
		t.Fatalf("只该有 create + patch 两条，实际 %d: %+v", len(refs), refs)
	}
	for _, r := range refs {
		if r.ID != "a1" || r.Type != "html" {
			t.Fatalf("引用不对: %+v", r)
		}
		if r.Title != "10.18 手账" || r.Summary != "一周记录" {
			t.Fatalf("标题摘要应带出（done 事件要用）: %+v", r)
		}
	}
}

// 一轮里 patch 同一页多次只留一张卡，标题以最后一次为准。
func TestMergeArtifactRefsDedupeByID(t *testing.T) {
	old := []model.ArtifactRef{{ID: "a1", Type: "html", Title: "旧标题"}}
	got := mergeArtifactRefs(old, []model.ArtifactRef{
		{ID: "a2", Type: "html", Title: "第二页"},
		{ID: "a1", Type: "html", Title: "新标题"},
	})
	if len(got) != 2 {
		t.Fatalf("应去重成 2 条，实际 %d: %+v", len(got), got)
	}
	if got[0].ID != "a1" || got[0].Title != "新标题" {
		t.Fatalf("a1 应被覆盖且留在首位: %+v", got)
	}
	if got[1].ID != "a2" {
		t.Fatalf("顺序应保持首次出现: %+v", got)
	}
}

// 落库只存 id/type：标题摘要改动不该把消息表写脏。
func TestCompactArtifactRefsDropsTitleSummary(t *testing.T) {
	got := compactArtifactRefs([]model.ArtifactRef{{ID: "a1", Type: "html", Title: "t", Summary: "s"}})
	if len(got) != 1 || got[0].ID != "a1" || got[0].Title != "" || got[0].Summary != "" {
		t.Fatalf("应只保留 id/type: %+v", got)
	}
}
