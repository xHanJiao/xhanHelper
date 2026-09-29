package agent

import (
	"reflect"
	"testing"

	"xhan-helper/internal/agent/ctxflow"
	"xhan-helper/internal/agent/retrieval"
)

// 口径 B：只有「本轮 fresh 命中且进入最终注入上下文」的记忆才计数。
func TestSelectInjectedMemoryIDs(t *testing.T) {
	mem := func(id string) retrieval.ContextPacket {
		return retrieval.ContextPacket{ID: id, Type: ctxflow.TypeMemory}
	}
	note := func(id string) retrieval.ContextPacket {
		return retrieval.ContextPacket{ID: id, Type: ctxflow.TypeNote}
	}

	tests := []struct {
		name     string
		zoneXML  string
		zonePkts []retrieval.ContextPacket
		fresh    map[string]bool
		packets  map[string][]retrieval.ContextPacket
		want     []string
	}{
		{
			name:     "zone: 只计 fresh 且进最终区的记忆",
			zoneXML:  "<memories/>",
			zonePkts: []retrieval.ContextPacket{mem("a"), mem("x"), note("n1")},
			fresh:    map[string]bool{"a": true, "b": true},
			want:     []string{"a"},
		},
		{
			// b 本轮 fresh 命中，但被 quota/budget 裁掉（不在 zonePkts）→ 不计
			name:     "zone: 被 budget 裁掉的 fresh 不计",
			zoneXML:  "<memories/>",
			zonePkts: []retrieval.ContextPacket{mem("a")},
			fresh:    map[string]bool{"a": true, "b": true},
			want:     []string{"a"},
		},
		{
			// a 只由上一轮携带（不在本轮 fresh）→ 不计
			name:     "zone: 仅携带的记忆不计",
			zoneXML:  "<memories/>",
			zonePkts: []retrieval.ContextPacket{mem("a")},
			fresh:    map[string]bool{},
			want:     nil,
		},
		{
			name:     "zone: 重复 id 去重",
			zoneXML:  "<memories/>",
			zonePkts: []retrieval.ContextPacket{mem("a"), mem("a")},
			fresh:    map[string]bool{"a": true},
			want:     []string{"a"},
		},
		{
			name:    "非 zone：常规合并的记忆全计",
			zoneXML: "",
			fresh:   map[string]bool{"a": true, "b": true},
			packets: map[string][]retrieval.ContextPacket{
				ctxflow.TypeMemory: {mem("a"), mem("b")},
				ctxflow.TypeNote:   {note("n1")},
			},
			want: []string{"a", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectInjectedMemoryIDs(tt.zoneXML, tt.zonePkts, tt.fresh, tt.packets)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ids = %v, want %v", got, tt.want)
			}
		})
	}
}
