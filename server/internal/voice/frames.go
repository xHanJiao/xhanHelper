package voice

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

// 豆包流式 ASR 的 WebSocket 二进制协议（官方文档：大模型流式语音识别API）。
//
//	byte0: protocol version(高4位) + header size(低4位，单位 4 字节)
//	byte1: message type(高4位) + message type specific flags(低4位)
//	byte2: serialization(高4位) + compression(低4位)
//	byte3: reserved
//	[4字节 sequence（仅 flags 为 0b0001 / 0b0011 时存在）]
//	4字节 payload size（大端）
//	payload
const (
	protocolVersion1 = 0x1
	headerSize4      = 0x1

	msgFullClientRequest  = 0x1
	msgAudioOnlyRequest   = 0x2
	msgFullServerResponse = 0x9
	msgErrorResponse      = 0xF

	flagNone             = 0x0
	flagPositiveSequence = 0x1
	flagLastNoSequence   = 0x2
	flagLastSequence     = 0x3

	serializationNone = 0x0
	serializationJSON = 0x1

	compressionNone = 0x0
	compressionGzip = 0x1
)

// asrFrame 是解码后的一帧豆包 ASR 消息。
type asrFrame struct {
	Type         byte
	Flags        byte
	Payload      []byte
	ErrorCode    int32
	ErrorMessage string
}

// encodeFrame 构造一个豆包 ASR 二进制帧；payload 按 compression 压缩。
func encodeFrame(msgType, flags, serialization, compression byte, payload []byte) ([]byte, error) {
	body := payload
	if compression == compressionGzip {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write(payload); err != nil {
			return nil, fmt.Errorf("gzip payload: %w", err)
		}
		if err := zw.Close(); err != nil {
			return nil, fmt.Errorf("close gzip: %w", err)
		}
		body = buf.Bytes()
	}

	headerLen := int(headerSize4) * 4
	out := make([]byte, headerLen+4+len(body))
	out[0] = protocolVersion1<<4 | headerSize4
	out[1] = msgType<<4 | flags
	out[2] = serialization<<4 | compression
	out[3] = 0
	binary.BigEndian.PutUint32(out[headerLen:headerLen+4], uint32(len(body)))
	copy(out[headerLen+4:], body)
	return out, nil
}

// decodeFrame 解析一个豆包 ASR 二进制帧。
func decodeFrame(data []byte) (*asrFrame, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("frame too short: %d bytes", len(data))
	}
	headerLen := int(data[0]&0x0f) * 4
	if headerLen < 4 || len(data) < headerLen+4 {
		return nil, fmt.Errorf("invalid header size %d (frame %d bytes)", headerLen, len(data))
	}
	f := &asrFrame{
		Type:  data[1] >> 4,
		Flags: data[1] & 0x0f,
	}
	compression := data[2] & 0x0f

	offset := headerLen
	if f.Flags == flagPositiveSequence || f.Flags == flagLastSequence {
		if len(data) < offset+4 {
			return nil, fmt.Errorf("missing sequence number")
		}
		// 序号只用于协议完整性，调用方不需要；跳过 4 字节
		offset += 4
	}
	// 错误响应比普通帧多一个 4 字节错误码，且错误码在 payload size **之前**：
	//   header(4) + 错误码(4) + payload size(4) + payload(JSON)
	// 按普通帧解析会把错误码当成 payload size（如 45000081 → 「payload 超过帧长」）。
	if f.Type == msgErrorResponse {
		if len(data) < offset+8 {
			return nil, fmt.Errorf("error frame too short: %d bytes", len(data))
		}
		f.ErrorCode = int32(binary.BigEndian.Uint32(data[offset : offset+4]))
		offset += 4
		payloadSize := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		offset += 4
		if len(data) < offset+payloadSize {
			return nil, fmt.Errorf("error payload size %d exceeds frame %d bytes", payloadSize, len(data))
		}
		body, err := decompress(data[offset:offset+payloadSize], compression)
		if err != nil {
			return nil, fmt.Errorf("error frame payload: %w", err)
		}
		f.Payload = body
		f.ErrorMessage = strings.TrimSpace(string(body))
		return f, nil
	}

	if len(data) < offset+4 {
		return nil, fmt.Errorf("missing payload size")
	}
	payloadSize := int(binary.BigEndian.Uint32(data[offset : offset+4]))
	offset += 4
	if len(data) < offset+payloadSize {
		return nil, fmt.Errorf("payload size %d exceeds frame %d bytes", payloadSize, len(data))
	}
	payload, err := decompress(data[offset:offset+payloadSize], compression)
	if err != nil {
		return nil, err
	}
	f.Payload = payload
	return f, nil
}

// decompress 按帧头的 compression 位解压 payload。
// 错误帧同样带这个位：此前错误分支直接当字符串转，gzip 化成乱码。
func decompress(payload []byte, compression byte) ([]byte, error) {
	if compression != compressionGzip {
		return payload, nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("open gzip payload: %w", err)
	}
	raw, err := io.ReadAll(zr)
	closeErr := zr.Close()
	if err != nil {
		return nil, fmt.Errorf("read gzip payload: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close gzip payload: %w", closeErr)
	}
	return raw, nil
}
