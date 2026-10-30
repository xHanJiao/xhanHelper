package agent

import (
	"encoding/json"
	"testing"

	"xhan-helper/internal/tool"
)

// 只有「写页面」（create / patch）才挂卡：get 的 outline / block / full 也带
// artifact_id，但读一下不该在聊天里多一张卡。
func TestArtifactRefsFromResultsOnlyCreateAndPatch(t *testing.T) {
	data, err := json.Marshal(map[string]string{"artifact_id": "a1", "type": "html"})
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
	}
}
