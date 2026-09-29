package model

import (
	"fmt"
	"strings"
	"testing"
)

func TestChunkVectorKeyRoundTrip(t *testing.T) {
	cases := []struct {
		parentType string
		parentID   string
		idx        int
	}{
		{EntityTypeNote, "abc-123", 0},
		{EntityTypeMemory, "m1", 12},
		{EntityTypeTheme, "t9", 3},
	}
	for _, c := range cases {
		key := ChunkVectorKey(c.parentType, c.parentID, c.idx)
		want := fmt.Sprintf("%s%s/%d", ChunkVectorKeyPrefix(c.parentType), c.parentID, c.idx)
		if key != want {
			t.Errorf("ChunkVectorKey = %q, want %q", key, want)
		}
		pt, pid := ParseChunkVectorKey(key)
		if pt != c.parentType || pid != c.parentID {
			t.Errorf("ParseChunkVectorKey(%q) = (%q, %q), want (%q, %q)", key, pt, pid, c.parentType, c.parentID)
		}
	}
}

func TestFullVectorPrefix(t *testing.T) {
	if p, ok := FullVectorPrefix(EntityTypeNote); !ok || p != VectorPrefixNote {
		t.Errorf("note prefix = %q/%v", p, ok)
	}
	if p, ok := FullVectorPrefix(EntityTypeMemory); !ok || p != VectorPrefixMemory {
		t.Errorf("memory prefix = %q/%v", p, ok)
	}
	if _, ok := FullVectorPrefix(EntityTypeChunk); ok {
		t.Error("chunk should not have a full vector prefix")
	}
}

// 钉住两张词表的契约：多数同值，但 mem≠memory、msg≠message；
// 前缀一律以冒号结尾。有人"统一"这两套词表会在这里红。
func TestVectorVocabInvariants(t *testing.T) {
	if VectorTypeMemory == EntityTypeMemory {
		t.Error(`VectorTypeMemory must stay "mem", not "memory"`)
	}
	if VectorTypeMessage == EntityTypeMessage {
		t.Error(`VectorTypeMessage must stay "msg", not "message"`)
	}
	if VectorTypeMemory != "mem" || VectorTypeMessage != "msg" {
		t.Errorf("mem/msg drifted: memory=%q message=%q", VectorTypeMemory, VectorTypeMessage)
	}
	prefixes := map[string]string{
		"note": VectorPrefixNote, "memory": VectorPrefixMemory, "chunk": VectorPrefixChunk,
		"message": VectorPrefixMessage, "summary": VectorPrefixSummary,
		"theme": VectorPrefixTheme, "tool": VectorPrefixTool,
	}
	for name, p := range prefixes {
		if !strings.HasSuffix(p, ":") {
			t.Errorf("prefix %s = %q, want trailing colon", name, p)
		}
	}
}
