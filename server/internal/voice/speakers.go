package voice

// Speaker — 可选的 TTS 音色，随 /api/voice/config 下发。
// 豆包语音合成 2.0 的音色与 resource_id: seed-tts-2.0 绑定。
type Speaker struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// curatedSpeakers 是精选音色：覆盖男女声与常用风格。
// 扩充只需在这里加一行，客户端下拉列表会跟着变。
var curatedSpeakers = []Speaker{
	{"zh_female_vv_uranus_bigtts", "Vivi 2.0"},
	{"zh_female_xiaohe_uranus_bigtts", "小何 2.0"},
	{"zh_male_m191_uranus_bigtts", "云舟 2.0"},
	{"zh_male_taocheng_uranus_bigtts", "小天 2.0"},
	{"zh_female_tuanzi_uranus_bigtts", "团子 2.0"},
	{"zh_male_liufei_uranus_bigtts", "刘飞 2.0"},
	{"zh_female_sophie_uranus_bigtts", "魅力苏菲 2.0"},
	{"zh_female_qingxinnvsheng_uranus_bigtts", "清新女声 2.0"},
	{"zh_male_ruyaqingnian_uranus_bigtts", "儒雅青年 2.0"},
	{"zh_female_wenroushunv_uranus_bigtts", "温柔淑女 2.0"},
	{"zh_female_zhixingnv_uranus_bigtts", "知性女声 2.0"},
	{"zh_male_shenyeboke_uranus_bigtts", "深夜播客 2.0"},
}

func isCuratedSpeaker(id string) bool {
	for _, s := range curatedSpeakers {
		if s.ID == id {
			return true
		}
	}
	return false
}

// SpeakerList 返回给客户端的音色列表：精选 + 配置默认。
// 默认音色不在精选里（如复刻音色）时补在首位，名字标为「默认（配置）」。
func SpeakerList(defaultID string) []Speaker {
	out := make([]Speaker, 0, len(curatedSpeakers)+1)
	if defaultID != "" && !isCuratedSpeaker(defaultID) {
		out = append(out, Speaker{ID: defaultID, Name: "默认（配置）"})
	}
	return append(out, curatedSpeakers...)
}

// ResolveSpeaker 解析本次请求该用哪个音色。
// fallback=true 表示请求的音色不在白名单，已回落默认（调用方记日志后继续）。
func ResolveSpeaker(defaultID, requested string) (speaker string, fallback bool) {
	if requested == "" {
		return defaultID, false
	}
	if requested == defaultID || isCuratedSpeaker(requested) {
		return requested, false
	}
	return defaultID, true
}
