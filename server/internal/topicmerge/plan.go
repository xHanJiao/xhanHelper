package topicmerge

import (
	"fmt"
	"sort"
	"strings"
)

// PlanRow — 映射文件里的一行合并指令（供人工审阅/编辑）。
type PlanRow struct {
	Cluster        int
	Representative string
	Member         string
	RefCount       int
	Similarity     float64
	Action         string // "merge" | "skip"
}

// Merge — 归约后的合并对：把 From 这个 topic 改名为 To。
type Merge struct {
	From string
	To   string
}

// Actionable 返回代表词有引用（RefCount>0）的簇。代表词引用为 0 意味着整簇都是
// 无引用的历史遗留 topic，交给 apply 的孤儿 GC 清理，不进报告与映射。
func Actionable(clusters []Cluster) []Cluster {
	out := make([]Cluster, 0, len(clusters))
	for _, c := range clusters {
		if len(c.Members) > 0 && c.Members[0].RefCount > 0 {
			out = append(out, c)
		}
	}
	return out
}

// BuildPlanRows 把聚类结果摊平成可编辑的行。代表词自身也占一行（apply 时忽略）。
func BuildPlanRows(clusters []Cluster) []PlanRow {
	var rows []PlanRow
	for i, c := range clusters {
		if len(c.Members) < 2 || c.Members[0].RefCount == 0 {
			continue
		}
		for _, m := range c.Members {
			rows = append(rows, PlanRow{
				Cluster:        i + 1,
				Representative: c.Representative,
				Member:         m.Value,
				RefCount:       m.RefCount,
				Similarity:     m.Similarity,
				Action:         "merge",
			})
		}
	}
	return rows
}

// RenderPlan 生成映射文件内容（TSV）。人工可改 representative / action，或删行。
func RenderPlan(userID string, rows []PlanRow) string {
	var b strings.Builder
	b.WriteString("# topic-merge plan —— 人工审阅后可编辑，勿改列顺序/制表符\n")
	b.WriteString("# user_id: " + userID + "\n")
	b.WriteString("# action=merge：把 member 的 topic 改为 representative；action=skip：跳过该成员；删除整行=跳过\n")
	b.WriteString("# 注意：representative 必须是本簇已有成员，不能凭空造新词\n")
	b.WriteString("cluster\trepresentative\tmember\tref_count\tsimilarity\taction\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%d\t%s\t%s\t%d\t%.3f\t%s\n",
			r.Cluster, r.Representative, r.Member, r.RefCount, r.Similarity, r.Action)
	}
	return b.String()
}

// ParsePlan 解析映射文件，返回 user_id 与归约后的合并对。
// 同一 member 出现两个不同 representative 时报错（人工编辑冲突）。
func ParsePlan(text string) (userID string, merges []Merge, err error) {
	target := make(map[string]string) // member -> representative
	for i, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if v, ok := strings.CutPrefix(line, "# user_id:"); ok {
				userID = strings.TrimSpace(v)
			}
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 6 {
			return "", nil, fmt.Errorf("第 %d 行字段数不足（需 6 列，用制表符分隔）: %q", i+1, line)
		}
		rep := strings.TrimSpace(fields[1])
		member := strings.TrimSpace(fields[2])
		action := strings.TrimSpace(fields[5])
		if rep == "" || member == "" {
			return "", nil, fmt.Errorf("第 %d 行 representative/member 为空", i+1)
		}
		if action != "merge" {
			continue
		}
		if member == rep {
			continue
		}
		if prev, ok := target[member]; ok && prev != rep {
			return "", nil, fmt.Errorf("第 %d 行冲突：%q 同时被合并到 %q 与 %q", i+1, member, prev, rep)
		}
		target[member] = rep
	}
	if userID == "" {
		return "", nil, fmt.Errorf("映射文件缺少 '# user_id:' 行")
	}
	for member, rep := range target {
		merges = append(merges, Merge{From: member, To: rep})
	}
	sort.Slice(merges, func(i, j int) bool { return merges[i].From < merges[j].From })
	return userID, merges, nil
}

// ThresholdStat — 某阈值下的聚类规模，用于报告里的敏感性对比。
type ThresholdStat struct {
	Threshold    float64
	Clusters     int
	MultiMember  int
	MergedTopics int
}

// StatOf 汇总某阈值下的聚类规模。
func StatOf(clusters []Cluster, threshold float64) ThresholdStat {
	return ThresholdStat{
		Threshold:    threshold,
		Clusters:     len(clusters),
		MultiMember:  MultiMemberCount(clusters),
		MergedTopics: MergedTopicCount(clusters),
	}
}

// RenderReport 生成 Markdown 报告。
func RenderReport(userID string, threshold float64, clusters []Cluster, sensitivity []ThresholdStat, noVector []string) string {
	active := Actionable(clusters)

	topicCount := 0
	for _, c := range clusters {
		topicCount += len(c.Members)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# topic 收敛报告\n\n")
	fmt.Fprintf(&b, "- user: `%s`\n", userID)
	fmt.Fprintf(&b, "- 阈值: %.2f（leader 聚类，成员只跟簇代表比）\n", threshold)
	fmt.Fprintf(&b, "- 参与聚类 topic: %d 个 → %d 簇（有引用 %d，0 引用孤儿 %d）\n",
		topicCount, len(clusters), len(active), len(clusters)-len(active))
	fmt.Fprintf(&b, "- 多成员簇: %d 个｜涉及改名 topic: %d 个\n\n",
		MultiMemberCount(active), MergedTopicCount(active))

	b.WriteString("## 阈值敏感性（含孤儿）\n\n")
	b.WriteString("| 阈值 | 簇数 | 多成员簇 | 改名 topic 数 |\n|---|---|---|---|\n")
	for _, s := range sensitivity {
		fmt.Fprintf(&b, "| %.2f | %d | %d | %d |\n", s.Threshold, s.Clusters, s.MultiMember, s.MergedTopics)
	}
	b.WriteString("\n")

	b.WriteString("## 多成员簇（有引用）\n\n")
	n := 0
	for _, c := range active {
		if len(c.Members) < 2 {
			continue
		}
		n++
		fmt.Fprintf(&b, "### %d. 代表词：%s（引用 %d）\n\n", n, c.Representative, c.Members[0].RefCount)
		for _, m := range c.Members[1:] {
			fmt.Fprintf(&b, "- %s（引用 %d，相似度 %.3f）\n", m.Value, m.RefCount, m.Similarity)
		}
		b.WriteString("\n")
	}

	var singles []string
	for _, c := range active {
		if len(c.Members) == 1 {
			singles = append(singles, c.Representative)
		}
	}
	fmt.Fprintf(&b, "## 单成员簇（%d 个，无需合并）\n\n%s\n\n", len(singles), strings.Join(singles, "、"))

	var orphans []string
	for _, c := range clusters {
		if len(c.Members) > 0 && c.Members[0].RefCount == 0 {
			orphans = append(orphans, c.Representative)
		}
	}
	if len(orphans) > 0 {
		fmt.Fprintf(&b, "## 0 引用孤儿（%d 个，apply 时 GC 清理）\n\n%s\n\n", len(orphans), strings.Join(orphans, "、"))
	}

	if len(noVector) > 0 {
		fmt.Fprintf(&b, "## 无向量、未参与聚类（%d 个）\n\n%s\n\n", len(noVector), strings.Join(noVector, "、"))
	}
	return b.String()
}
