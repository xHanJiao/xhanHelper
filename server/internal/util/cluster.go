package util

import "math"

// ClusterVectors 将一组向量按余弦相似度聚类。
// vecs: name → vector，threshold: 相似度阈值（0~1）。
// 使用并查集（Union-Find）算法合并相似对，O(N²) 时间，N<100 时足够快。
// 返回每个 cluster 的主名称和成员列表。
func ClusterVectors(vecs map[string][]float64, threshold float64) []Cluster {
	topics := make([]string, 0, len(vecs))
	for t := range vecs {
		topics = append(topics, t)
	}

	parent := make([]int, len(topics))
	for i := range parent {
		parent[i] = i
	}

	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}

	for i := 0; i < len(topics); i++ {
		for j := i + 1; j < len(topics); j++ {
			if CosineSimilarity(vecs[topics[i]], vecs[topics[j]]) >= threshold {
				pi, pj := find(i), find(j)
				if pi != pj {
					parent[pi] = pj
				}
			}
		}
	}

	groups := make(map[int][]string)
	for i, t := range topics {
		root := find(i)
		groups[root] = append(groups[root], t)
	}

	result := make([]Cluster, 0, len(groups))
	for _, members := range groups {
		mainTopic := members[0]
		result = append(result, Cluster{MainTopic: mainTopic, Members: members})
	}
	return result
}

// Cluster — 一个聚类结果，MainTopic 为出现顺序第一的成员名
type Cluster struct {
	MainTopic string   // 聚类主名称
	Members   []string // 该聚类的所有成员名称
}

// CosineSimilarity 计算两个等长向量之间的余弦相似度，返回 [-1, 1]。
// 输入：两个等长的 float64 向量 a 和 b。
// 任意向量为空、长度不等或范数为零时，返回 0。
func CosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
