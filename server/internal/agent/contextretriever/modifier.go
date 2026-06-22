package contextretriever

import "xhan-helper/internal/agent/retrieval"

// ScoreModifier 对 ContextPacket 的分数字段做变换，用于提权/降权。
type ScoreModifier func(pkt retrieval.ContextPacket) retrieval.ContextPacket

// BoostFactor 返回一个 ScoreModifier，将 Score 乘以 factor，
// 结果 clamp 到 [0, 1]。factor>1 提权，0<factor<1 降权。
func BoostFactor(factor float64) ScoreModifier {
	return func(pkt retrieval.ContextPacket) retrieval.ContextPacket {
		pkt.Score = clamp01(pkt.Score * factor)
		return pkt
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
