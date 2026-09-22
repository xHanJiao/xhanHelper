package entitygc

import (
	"reflect"
	"testing"
)

func TestDiff(t *testing.T) {
	valid := toSet([]string{"n1", "n2", "n3"})
	got := diff([]string{"n1", "n9", "n2", "n9", "n8"}, valid)
	want := []string{"n9", "n8"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("diff = %v, want %v", got, want)
	}
}

func TestDiffAllValid(t *testing.T) {
	valid := toSet([]string{"a", "b"})
	if got := diff([]string{"a", "b"}, valid); len(got) != 0 {
		t.Errorf("diff = %v, want empty", got)
	}
}

func TestUnion(t *testing.T) {
	got := union([]string{"a", "b"}, []string{"b", "c"})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("union = %v, want %v", got, want)
	}
}

func TestUnionDoesNotMutateInput(t *testing.T) {
	a := []string{"a"}
	_ = union(a, []string{"b"})
	if !reflect.DeepEqual(a, []string{"a"}) {
		t.Errorf("input mutated: %v", a)
	}
}
