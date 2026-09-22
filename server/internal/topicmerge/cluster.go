// Package topicmerge 提供 topic 存量收敛工具：把同义 topic 合并成规范词。
//
// 分两步：Generate 只读地产出报告 + 可编辑的映射文件；Apply 读取人工确认后的
// 映射执行合并，并顺带清理孤儿 topic 概念。
package topicmerge

import (
	"sort"

	"xhan-helper/internal/util"
)

// Topic — 一个待聚类的 topic 及其向量与引用数（notes + memories）。
type Topic struct {
	Value    string
	Vector   []float64
	RefCount int
}

// Member — 簇内成员。
type Member struct {
	Value      string
	RefCount   int
	Similarity float64 // 与簇代表词的余弦相似度；代表词自身为 1
}

// Cluster — leader 聚类结果，代表词为 Members[0]。
type Cluster struct {
	Representative string
	Members        []Member
}

// LeaderCluster 对 topics 做 leader 聚类：每个 topic 只与已有簇的代表比较，
// 相似度 ≥ threshold 则并入最相似的代表，否则自成一簇；不对成员之间传递。
// 这样宁可分得细，也不会像并查集那样把不相关的词连成巨簇。
//
// 排序保证确定性：引用数降序 → 词长升序 → 字典序升序。因此每簇首个成员
// 即「引用数最高、并列取最短」的代表词。
func LeaderCluster(topics []Topic, threshold float64) []Cluster {
	sorted := make([]Topic, len(topics))
	copy(sorted, topics)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.RefCount != b.RefCount {
			return a.RefCount > b.RefCount
		}
		la, lb := len([]rune(a.Value)), len([]rune(b.Value))
		if la != lb {
			return la < lb
		}
		return a.Value < b.Value
	})

	type leader struct {
		value string
		vec   []float64
	}
	var leaders []leader
	owner := make([]int, 0, len(sorted))
	sim := make([]float64, 0, len(sorted))

	for _, t := range sorted {
		best, bestSim := -1, threshold
		for j, l := range leaders {
			if s := util.CosineSimilarity(t.Vector, l.vec); s >= bestSim {
				best, bestSim = j, s
			}
		}
		if best < 0 {
			leaders = append(leaders, leader{value: t.Value, vec: t.Vector})
			owner = append(owner, len(leaders)-1)
			sim = append(sim, 1)
			continue
		}
		owner = append(owner, best)
		sim = append(sim, bestSim)
	}

	clusters := make([]Cluster, len(leaders))
	for j, l := range leaders {
		clusters[j].Representative = l.value
	}
	for i, t := range sorted {
		c := &clusters[owner[i]]
		c.Members = append(c.Members, Member{Value: t.Value, RefCount: t.RefCount, Similarity: sim[i]})
	}

	sort.SliceStable(clusters, func(i, j int) bool {
		if len(clusters[i].Members) != len(clusters[j].Members) {
			return len(clusters[i].Members) > len(clusters[j].Members)
		}
		return clusters[i].Representative < clusters[j].Representative
	})
	return clusters
}

// MultiMemberCount 返回含 ≥2 个成员的簇数。
func MultiMemberCount(clusters []Cluster) int {
	n := 0
	for _, c := range clusters {
		if len(c.Members) > 1 {
			n++
		}
	}
	return n
}

// MergedTopicCount 返回会被改名的 topic 数（各簇成员数 -1 之和）。
func MergedTopicCount(clusters []Cluster) int {
	n := 0
	for _, c := range clusters {
		if len(c.Members) > 1 {
			n += len(c.Members) - 1
		}
	}
	return n
}
