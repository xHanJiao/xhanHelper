package agent

import "testing"

// TestVectorStore_Float32RoundTrip：Upsert(float64) → Search 排序与预期一致，GetVector 可读回。
func TestVectorStore_Float32RoundTrip(t *testing.T) {
	vs := NewVectorStore()
	vs.Upsert("a", []float64{1, 0, 0}, nil)
	vs.Upsert("b", []float64{0, 1, 0}, nil)
	vs.Upsert("c", []float64{0.9, 0.1, 0}, nil)

	res := vs.Search([]float64{1, 0, 0}, 2, nil)
	if len(res) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res))
	}
	if res[0].ID != "a" || res[1].ID != "c" {
		t.Fatalf("unexpected order: %s, %s", res[0].ID, res[1].ID)
	}
	if v, ok := vs.GetVector("a"); !ok || len(v) != 3 {
		t.Fatalf("GetVector failed: %v %v", v, ok)
	}
}

// TestVectorStore_DeleteByIDs：批量删除命中项，未列出的保留。
func TestVectorStore_DeleteByIDs(t *testing.T) {
	vs := NewVectorStore()
	vs.Upsert("msg:1", []float64{1, 0}, map[string]string{"type": "msg"})
	vs.Upsert("msg:2", []float64{0, 1}, map[string]string{"type": "msg"})
	vs.Upsert("msg:3", []float64{1, 1}, map[string]string{"type": "msg"})

	vs.DeleteByIDs([]string{"msg:1", "msg:3", "msg:absent"})

	if vs.Size() != 1 {
		t.Fatalf("expected size 1, got %d", vs.Size())
	}
	if _, ok := vs.GetVector("msg:2"); !ok {
		t.Fatal("msg:2 should remain")
	}
}
