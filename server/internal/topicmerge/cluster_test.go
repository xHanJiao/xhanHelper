package topicmerge

import (
	"strings"
	"testing"
)

func vec(pairs ...float64) []float64 { return pairs }

func TestLeaderClusterJoinsSimilarToLeader(t *testing.T) {
	topics := []Topic{
		{Value: "睡眠", Vector: vec(1, 0), RefCount: 185},
		{Value: "睡眠管理", Vector: vec(1, 0.2), RefCount: 103},
		{Value: "睡眠与作息", Vector: vec(1, 0.5), RefCount: 5},
	}
	clusters := LeaderCluster(topics, 0.70)
	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d: %+v", len(clusters), clusters)
	}
	c := clusters[0]
	if c.Representative != "睡眠" {
		t.Errorf("representative = %q, want 睡眠", c.Representative)
	}
	if len(c.Members) != 3 {
		t.Fatalf("members = %d, want 3", len(c.Members))
	}
	if c.Members[0].Value != "睡眠" || c.Members[0].Similarity != 1 {
		t.Errorf("members[0] = %+v, want 睡眠/1.0", c.Members[0])
	}
}

// 非传递性：A~B、B~C 都超阈值，但 A 与 C 不相似。leader 聚类只让成员跟代表比，
// C 不会因为 B 而入簇。
func TestLeaderClusterNotTransitive(t *testing.T) {
	topics := []Topic{
		{Value: "A", Vector: vec(1, 0), RefCount: 10},
		{Value: "B", Vector: vec(1, 1), RefCount: 8},
		{Value: "C", Vector: vec(0, 1), RefCount: 6},
	}
	clusters := LeaderCluster(topics, 0.70)
	if len(clusters) != 2 {
		t.Fatalf("expected 2 clusters, got %d: %+v", len(clusters), clusters)
	}
	if clusters[0].Representative != "A" || len(clusters[0].Members) != 2 {
		t.Errorf("cluster0 = %+v, want A with A,B", clusters[0])
	}
	if clusters[1].Representative != "C" || len(clusters[1].Members) != 1 {
		t.Errorf("cluster1 = %+v, want C alone", clusters[1])
	}
}

func TestRepresentativeTieBreak(t *testing.T) {
	// 同引用数 → 取字符最短；再并列 → 字典序。
	topics := []Topic{
		{Value: "睡眠管理", Vector: vec(1, 0), RefCount: 5},
		{Value: "睡眠", Vector: vec(1, 0), RefCount: 5},
		{Value: "bb", Vector: vec(1, 0), RefCount: 3},
		{Value: "aa", Vector: vec(1, 0), RefCount: 3},
	}
	clusters := LeaderCluster(topics, 0.70)
	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(clusters))
	}
	if clusters[0].Representative != "睡眠" {
		t.Errorf("representative = %q, want 睡眠 (shorter)", clusters[0].Representative)
	}
	// aa 与 睡眠 同长但引用数低，故不代表；这里只断言代表选对即可。
}

func TestStats(t *testing.T) {
	clusters := []Cluster{
		{Representative: "x", Members: []Member{{Value: "x"}, {Value: "y"}, {Value: "z"}}},
		{Representative: "s", Members: []Member{{Value: "s"}}},
	}
	if got := MultiMemberCount(clusters); got != 1 {
		t.Errorf("MultiMemberCount = %d, want 1", got)
	}
	if got := MergedTopicCount(clusters); got != 2 {
		t.Errorf("MergedTopicCount = %d, want 2", got)
	}
}

func TestPlanRoundTrip(t *testing.T) {
	clusters := []Cluster{
		{Representative: "睡眠", Members: []Member{
			{Value: "睡眠", RefCount: 185, Similarity: 1},
			{Value: "睡眠管理", RefCount: 103, Similarity: 0.92},
			{Value: "睡眠与作息", RefCount: 5, Similarity: 0.81},
		}},
		{Representative: "独苗", Members: []Member{{Value: "独苗", RefCount: 1, Similarity: 1}}},
		// 代表词 0 引用 → 整簇孤儿，不进映射（交给 GC）。
		{Representative: "垃圾A", Members: []Member{
			{Value: "垃圾A", RefCount: 0, Similarity: 1},
			{Value: "垃圾B", RefCount: 0, Similarity: 0.9},
		}},
	}
	rows := BuildPlanRows(clusters)
	// 单成员簇、0 引用簇都不产出行。
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	text := RenderPlan("user-1", rows)
	uid, merges, err := ParsePlan(text)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if uid != "user-1" {
		t.Errorf("userID = %q", uid)
	}
	if len(merges) != 2 {
		t.Fatalf("merges = %d, want 2 (睡眠 self 行忽略): %+v", len(merges), merges)
	}
	if merges[0] != (Merge{From: "睡眠与作息", To: "睡眠"}) || merges[1] != (Merge{From: "睡眠管理", To: "睡眠"}) {
		t.Errorf("merges = %+v", merges)
	}
}

func TestParsePlanSkipAndConflict(t *testing.T) {
	skip := strings.Join([]string{
		"# user_id: u1",
		"cluster\trepresentative\tmember\tref_count\tsimilarity\taction",
		"1\t睡眠\t睡眠\t5\t1.0\tmerge",
		"1\t睡眠\t睡眠管理\t3\t0.9\tmerge",
		"1\t睡眠\t睡眠习惯\t2\t0.8\tskip",
	}, "\n")
	uid, merges, err := ParsePlan(skip)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if uid != "u1" || len(merges) != 1 || merges[0].From != "睡眠管理" {
		t.Fatalf("uid=%q merges=%+v", uid, merges)
	}

	conflict := strings.Join([]string{
		"# user_id: u1",
		"1\t睡眠\t睡眠管理\t3\t0.9\tmerge",
		"1\t其它\t睡眠管理\t3\t0.9\tmerge",
	}, "\n")
	if _, _, err := ParsePlan(conflict); err == nil {
		t.Fatal("expected conflict error, got nil")
	}

	if _, _, err := ParsePlan("1\t睡眠\t睡眠管理\t3\t0.9\tmerge"); err == nil {
		t.Fatal("expected missing user_id error, got nil")
	}
}
