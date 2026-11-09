package config

// VoiceConfig — 语音聊天（豆包 TTS / 流式 ASR）配置。
// 详见 docs/design/voice-chat/design.md。
//
// 默认关闭；enabled=true 且 api_key 非空时才注册可用的语音能力。
// 新版火山控制台只需要 X-Api-Key（不需要 AppID / AccessToken / Cluster）。
type VoiceConfig struct {
	Enabled bool           `yaml:"enabled"`
	APIKey  string         `yaml:"api_key"`
	TTS     VoiceTTSConfig `yaml:"tts"`
	ASR     VoiceASRConfig `yaml:"asr"`
}

// VoiceTTSConfig — 豆包语音合成模型 2.0（HTTP Chunked 单向流式）。
type VoiceTTSConfig struct {
	Endpoint       string `yaml:"endpoint"`
	ResourceID     string `yaml:"resource_id"`
	Speaker        string `yaml:"speaker"`
	Format         string `yaml:"format"`
	SampleRate     int    `yaml:"sample_rate"`
	SpeechRate     int    `yaml:"speech_rate"` // -50..100，0 为默认
	MaxChars       int    `yaml:"max_chars"`   // 单次合成文本上限，防止误送超长文本
	TimeoutSeconds int    `yaml:"timeout_seconds"`
}

// VoiceASRConfig — 豆包流式语音识别模型 2.0（WebSocket 二进制协议）。
type VoiceASRConfig struct {
	Endpoint       string `yaml:"endpoint"`
	ResourceID     string `yaml:"resource_id"`
	Language       string `yaml:"language"`
	MaxSeconds     int    `yaml:"max_seconds"`     // 单条连接内的音频上限（按 16k/16bit 字节数折算）
	SessionSeconds int    `yaml:"session_seconds"` // C 模式（连续对话）单次会话上限；超过则客户端需重新开启
	TimeoutSeconds int    `yaml:"timeout_seconds"`
	// 分句与返回方式：开启二遍识别后由服务端按静音判停，definite 分句才带最终文本。
	ResultType    string `yaml:"result_type"`     // full=累计全量；single=增量（不返回之前分句）
	EndWindowSize int    `yaml:"end_window_size"` // 静音判停阈值 ms，范围 [300,5000]
	// 声纹/说话人分离（默认关闭）。SSDMode=2 为短音频声纹识别，需配 VoiceprintID 或 VoiceprintGroupID。
	EnableSpeakerInfo bool   `yaml:"enable_speaker_info"`
	SSDMode           int    `yaml:"ssd_mode"`
	VoiceprintID      string `yaml:"voiceprint_id"`
	VoiceprintGroupID string `yaml:"voiceprint_group_id"`
}

// ApplyDefaults 填充语音配置默认值（仅零值）。
func (v *VoiceConfig) ApplyDefaults() {
	if v.TTS.Endpoint == "" {
		v.TTS.Endpoint = "https://openspeech.bytedance.com/api/v3/tts/unidirectional"
	}
	if v.TTS.ResourceID == "" {
		v.TTS.ResourceID = "seed-tts-2.0"
	}
	if v.TTS.Speaker == "" {
		v.TTS.Speaker = "zh_female_vv_uranus_bigtts"
	}
	if v.TTS.Format == "" {
		v.TTS.Format = "mp3"
	}
	if v.TTS.SampleRate == 0 {
		v.TTS.SampleRate = 24000
	}
	if v.TTS.MaxChars == 0 {
		v.TTS.MaxChars = 2000
	}
	if v.TTS.TimeoutSeconds == 0 {
		v.TTS.TimeoutSeconds = 90
	}
	if v.ASR.Endpoint == "" {
		v.ASR.Endpoint = "wss://openspeech.bytedance.com/api/v3/sauc/bigmodel_async"
	}
	if v.ASR.ResourceID == "" {
		v.ASR.ResourceID = "volc.seedasr.sauc.duration"
	}
	if v.ASR.Language == "" {
		v.ASR.Language = "zh-CN"
	}
	if v.ASR.ResultType == "" {
		v.ASR.ResultType = "full"
	}
	if v.ASR.EndWindowSize == 0 {
		v.ASR.EndWindowSize = 800
	}
	if v.ASR.MaxSeconds == 0 {
		v.ASR.MaxSeconds = 60
	}
	// C 模式的会话上限：流式 ASR 按音频时长计费（静音也计时），不能无限开着麦
	if v.ASR.SessionSeconds == 0 {
		v.ASR.SessionSeconds = 600
	}
	if v.ASR.TimeoutSeconds == 0 {
		v.ASR.TimeoutSeconds = 30
	}
}

// Ready 语音能力是否可用（启用且配置了 API Key）。
func (v *VoiceConfig) Ready() bool {
	return v.Enabled && v.APIKey != ""
}

// mergeVoice 把 src 的非零值覆盖到 dst（与 config.merge 的既有风格一致）。
func mergeVoice(dst, src *VoiceConfig) {
	if src.Enabled {
		dst.Enabled = true
	}
	if src.APIKey != "" {
		dst.APIKey = src.APIKey
	}
	if src.TTS.Endpoint != "" {
		dst.TTS.Endpoint = src.TTS.Endpoint
	}
	if src.TTS.ResourceID != "" {
		dst.TTS.ResourceID = src.TTS.ResourceID
	}
	if src.TTS.Speaker != "" {
		dst.TTS.Speaker = src.TTS.Speaker
	}
	if src.TTS.Format != "" {
		dst.TTS.Format = src.TTS.Format
	}
	if src.TTS.SampleRate != 0 {
		dst.TTS.SampleRate = src.TTS.SampleRate
	}
	if src.TTS.SpeechRate != 0 {
		dst.TTS.SpeechRate = src.TTS.SpeechRate
	}
	if src.TTS.MaxChars != 0 {
		dst.TTS.MaxChars = src.TTS.MaxChars
	}
	if src.TTS.TimeoutSeconds != 0 {
		dst.TTS.TimeoutSeconds = src.TTS.TimeoutSeconds
	}
	if src.ASR.Endpoint != "" {
		dst.ASR.Endpoint = src.ASR.Endpoint
	}
	if src.ASR.ResourceID != "" {
		dst.ASR.ResourceID = src.ASR.ResourceID
	}
	if src.ASR.Language != "" {
		dst.ASR.Language = src.ASR.Language
	}
	if src.ASR.ResultType != "" {
		dst.ASR.ResultType = src.ASR.ResultType
	}
	if src.ASR.EndWindowSize != 0 {
		dst.ASR.EndWindowSize = src.ASR.EndWindowSize
	}
	if src.ASR.MaxSeconds != 0 {
		dst.ASR.MaxSeconds = src.ASR.MaxSeconds
	}
	if src.ASR.SessionSeconds != 0 {
		dst.ASR.SessionSeconds = src.ASR.SessionSeconds
	}
	if src.ASR.TimeoutSeconds != 0 {
		dst.ASR.TimeoutSeconds = src.ASR.TimeoutSeconds
	}
	if src.ASR.EnableSpeakerInfo {
		dst.ASR.EnableSpeakerInfo = true
	}
	if src.ASR.SSDMode != 0 {
		dst.ASR.SSDMode = src.ASR.SSDMode
	}
	if src.ASR.VoiceprintID != "" {
		dst.ASR.VoiceprintID = src.ASR.VoiceprintID
	}
	if src.ASR.VoiceprintGroupID != "" {
		dst.ASR.VoiceprintGroupID = src.ASR.VoiceprintGroupID
	}
}
