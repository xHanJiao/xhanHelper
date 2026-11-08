// voice-smoke — 豆包流式 ASR 联调工具。
//
// 把一个 16k/16bit/mono 的 PCM wav 按实时节拍喂给 ASR，逐条打印识别结果与耗时。
// 真机联调时用来判断「说完能否正常结束（空负包）」「enable_nonstream 是否重复下发」
// 这类光看 app 看不出来的协议问题。
//
// 用法:
//
//	afconvert -f WAVE -d LEI16@16000 -c 1 in.mp3 /tmp/t.wav   # 有 mp3/录音时先转 wav
//	DOUBAO_VOICE_API_KEY=... go run ./cmd/voice-smoke -wav /tmp/t.wav
package main

import (
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"xhan-helper/internal/config"
	"xhan-helper/internal/voice"
)

// chunkBytes — 200ms @ 16kHz/16bit/mono，与客户端上行粒度一致。
const chunkBytes = 6400

func main() {
	wav := flag.String("wav", "", "16kHz/16bit/mono PCM wav 文件")
	fast := flag.Bool("fast", false, "不按 200ms 节拍发送（一次性推完，只看结果不测时延）")
	resultType := flag.String("result-type", "", "full（累计全量，默认）| single（增量，不返回之前分句）")
	maxSeconds := flag.Int("max-seconds", 0, "单条连接的音频上限秒数，0=默认 60")
	endWindow := flag.Int("end-window", 0, "静音判停阈值 ms，0=默认 800（范围 300-5000）")
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, `voice-smoke — 豆包流式 ASR 联调

用法:
  voice-smoke -wav <file.wav> [-fast] [-result-type single] [-max-seconds 300] [-end-window 600]

需要环境变量 DOUBAO_VOICE_API_KEY。
wav 必须是 16kHz/16bit/mono；mp3 或其它采样率先转：
  afconvert -f WAVE -d LEI16@16000 -c 1 in.mp3 /tmp/t.wav

Flags:
`)
		flag.PrintDefaults()
	}
	flag.Parse()
	if *wav == "" {
		flag.Usage()
		os.Exit(2)
	}

	key := os.Getenv("DOUBAO_VOICE_API_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "缺少环境变量 DOUBAO_VOICE_API_KEY")
		os.Exit(1)
	}

	pcm, err := readWAV(*wav)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取 wav 失败:", err)
		os.Exit(1)
	}

	// 直接用默认值（与 conf/config.yaml 的 voice.asr 一致）；本工具不加载完整配置，避免依赖 DB/LLM key。
	vc := config.VoiceConfig{}
	vc.ApplyDefaults()
	cfg := vc.ASR
	if *resultType != "" {
		cfg.ResultType = *resultType
	}
	if *maxSeconds > 0 {
		cfg.MaxSeconds = *maxSeconds
	}
	if *endWindow > 0 {
		cfg.EndWindowSize = *endWindow
	}

	fmt.Printf("wav: %.2f 秒，endpoint=%s resource=%s\n", float64(len(pcm))/32000, cfg.Endpoint, cfg.ResourceID)
	fmt.Printf("result_type=%s end_window=%dms max_seconds=%d\n", cfg.ResultType, cfg.EndWindowSize, cfg.MaxSeconds)

	dialCtx, dialCancel := context.WithTimeout(context.Background(), time.Duration(cfg.TimeoutSeconds)*time.Second)
	start := time.Now()
	asr, err := voice.DialASR(dialCtx, cfg, key, "voice-smoke")
	dialCancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "握手失败:", err)
		os.Exit(1)
	}
	defer asr.Close()
	fmt.Printf("[+%5dms] 已连接，开始推送 PCM\n", time.Since(start).Milliseconds())

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// 上行与接收并行：否则看不到流式 partial，也测不出 final 的时延。
	sendErr := make(chan error, 1)
	go func() {
		for i := 0; i < len(pcm); i += chunkBytes {
			end := i + chunkBytes
			if end > len(pcm) {
				end = len(pcm)
			}
			if err := asr.SendAudio(pcm[i:end]); err != nil {
				sendErr <- fmt.Errorf("推送音频: %w", err)
				return
			}
			if !*fast {
				time.Sleep(200 * time.Millisecond)
			}
		}
		sendErr <- asr.Finish()
	}()

	var last string
	readErr := asr.Read(ctx, func(r voice.ASRResult) {
		tag := "partial"
		switch {
		case r.Final:
			tag = "final"
		case r.Definite:
			tag = "definite"
		}
		fmt.Printf("[+%5dms] %-8s %s\n", time.Since(start).Milliseconds(), tag, r.Text)
		if !r.Final && r.Text != "" && r.Text == last {
			fmt.Println("          ↑ 与上一条完全相同（疑似 enable_nonstream 重复下发）")
		}
		last = r.Text
	})

	if err := <-sendErr; err != nil {
		fmt.Fprintln(os.Stderr, "上行出错:", err)
	}
	if readErr != nil {
		fmt.Fprintln(os.Stderr, "接收失败:", readErr)
		os.Exit(1)
	}
	fmt.Printf("完成，总耗时 %dms，最终文本：%s\n", time.Since(start).Milliseconds(), last)
}

// readWAV 解析 RIFF/WAVE，返回 data 块；非 16k/16bit/mono 直接报错。
func readWAV(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < 12 || string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return nil, errors.New("不是 RIFF/WAVE 文件")
	}

	var (
		data                 []byte
		rate, bits, channels int
		seenFmt              bool
	)
	for off := 12; off+8 <= len(raw); {
		id := string(raw[off : off+4])
		size := int(binary.LittleEndian.Uint32(raw[off+4 : off+8]))
		body := raw[off+8:]
		if size > len(body) {
			size = len(body)
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, errors.New("fmt 块太短")
			}
			if format := binary.LittleEndian.Uint16(body[0:2]); format != 1 {
				return nil, fmt.Errorf("只支持 PCM（audioFormat=1），当前 %d，先用 afconvert 转", format)
			}
			channels = int(binary.LittleEndian.Uint16(body[2:4]))
			rate = int(binary.LittleEndian.Uint32(body[4:8]))
			bits = int(binary.LittleEndian.Uint16(body[14:16]))
			seenFmt = true
		case "data":
			data = body[:size]
		}
		off += 8 + size
		if size%2 == 1 {
			off++
		}
	}
	if !seenFmt || data == nil {
		return nil, errors.New("缺少 fmt 或 data 块")
	}
	if rate != 16000 || channels != 1 || bits != 16 {
		return nil, fmt.Errorf("需要 16kHz/16bit/mono，当前 %dHz/%dbit/%dch", rate, bits, channels)
	}
	return data, nil
}
