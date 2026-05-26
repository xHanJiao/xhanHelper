package retrieval

// bigramSet 将文本切分为 bigram 集合，用于中文关键词匹配
func bigramSet(text string) map[string]bool {
	runes := []rune(text)
	set := make(map[string]bool, len(runes))
	for i := 0; i < len(runes)-1; i++ {
		set[string(runes[i:i+2])] = true
	}
	return set
}

// bigramOverlap 计算 query bigram 与目标文本的重叠比例
func bigramOverlap(queryBigrams map[string]bool, target string) float64 {
	if len(queryBigrams) == 0 {
		return 0
	}
	runes := []rune(target)
	if len(runes) < 2 {
		return 0
	}
	var overlap int
	for i := 0; i < len(runes)-1; i++ {
		if queryBigrams[string(runes[i:i+2])] {
			overlap++
		}
	}
	return float64(overlap) / float64(len(queryBigrams))
}
