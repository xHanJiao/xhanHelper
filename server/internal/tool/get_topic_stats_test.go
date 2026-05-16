package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"xhan-helper/internal/util"
)

func loadTopicVectors(t *testing.T) map[string][]float64 {
	t.Helper()
	data, err := os.ReadFile("../../data/topic_vectors.json")
	if err != nil {
		t.Skipf("跳过：无法读取 data/topic_vectors.json（%v），先导出: mysql -u xhan -pREDACTED -h 127.0.0.1 xhan_helper_test -N -e \"SELECT c.value, e.embedding FROM concept_embeddings c JOIN embeddings e ON e.entity_type='concept' AND e.entity_id=c.id WHERE c.type='topic';\" | python3 -c \"import sys,json; d={}; [d.__setitem__(p[0], json.loads(p[1])) for p in (l.strip().split('\\t',1) for l in sys.stdin) if len(p)==2]; print(json.dumps(d,ensure_ascii=False,indent=2))\" > server/data/topic_vectors.json", err)
	}
	var vecs map[string][]float64
	if err := json.Unmarshal(data, &vecs); err != nil {
		t.Fatalf("解析 JSON 失败: %v", err)
	}
	return vecs
}

func TestClusterTopics(t *testing.T) {
	vecs := loadTopicVectors(t)
	if len(vecs) == 0 {
		t.Fatal("未加载到任何向量")
	}
	t.Logf("加载了 %d 个 topic 向量", len(vecs))

	thresholds := []float64{0.85, 0.80, 0.75, 0.70, 0.65}
	for _, th := range thresholds {
		t.Run(fmt.Sprintf("threshold=%.2f", th), func(t *testing.T) {
			clusters := util.ClusterVectors(vecs, th)
			t.Logf("\n=== threshold=%.2f: %d topics → %d clusters ===", th, len(vecs), len(clusters))
			for _, cl := range clusters {
				sort.Strings(cl.Members)
				t.Logf("  %s ← %s", cl.MainTopic, strings.Join(cl.Members, ", "))
			}
		})
	}
}

// 不需要 API key 的单元测试——用假向量验证聚类逻辑正确性
func TestClusterTopicsLogic(t *testing.T) {
	// 构造假向量：职业组高相似，睡眠组高相似，婴儿就医独立
	makeVec := func(vals ...float64) []float64 {
		v := make([]float64, 10)
		for i, val := range vals {
			if i < len(v) {
				v[i] = val
			}
		}
		return v
	}
	vecs := map[string][]float64{
		"职业规划": makeVec(0.9, 0.1, 0.0),
		"职业生涯": makeVec(0.8, 0.2, 0.0),
		"求职规划": makeVec(0.7, 0.3, 0.0),
		"睡眠管理": makeVec(0.0, 0.1, 0.4, 0.5),
		"早醒记录": makeVec(0.0, 0.2, 0.3, 0.5),
		"婴儿就医": makeVec(0.0, 0.0, 0.0, 0.0, 1.0),
	}

	clusterList := util.ClusterVectors(vecs, 0.80)
	if len(clusterList) != 3 {
		t.Errorf("expected 3 clusters, got %d: %+v", len(clusterList), clusterList)
	}
	for _, cl := range clusterList {
		t.Logf("cluster: %s ← %v", cl.MainTopic, cl.Members)
	}
}
