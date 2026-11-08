package voice

import (
	"encoding/binary"
	"encoding/json"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	payload := []byte(`{"hello":"world"}`)
	frame, err := encodeFrame(msgFullClientRequest, flagNone, serializationJSON, compressionGzip, payload)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := decodeFrame(frame)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Type != msgFullClientRequest || got.Flags != flagNone {
		t.Fatalf("header mismatch: type=%d flags=%d", got.Type, got.Flags)
	}
	if string(got.Payload) != string(payload) {
		t.Fatalf("payload mismatch: %s", got.Payload)
	}
}

func TestFrameLastFlag(t *testing.T) {
	frame, err := encodeFrame(msgAudioOnlyRequest, flagLastNoSequence, serializationNone, compressionGzip, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := decodeFrame(frame)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Flags != flagLastNoSequence || len(got.Payload) != 0 {
		t.Fatalf("unexpected last frame: flags=%d payload=%d", got.Flags, len(got.Payload))
	}
}

func TestDecodeErrorFrame(t *testing.T) {
	// 官方错误帧布局：header(4) + 错误码(4) + payload size(4) + JSON
	msg := []byte(`{"message":"等包超时"}`)
	frame := make([]byte, 4+4+4+len(msg))
	frame[0] = protocolVersion1<<4 | headerSize4
	frame[1] = msgErrorResponse<<4 | flagNone
	frame[2] = serializationJSON<<4 | compressionNone
	binary.BigEndian.PutUint32(frame[4:8], 45000081)
	binary.BigEndian.PutUint32(frame[8:12], uint32(len(msg)))
	copy(frame[12:], msg)

	got, err := decodeFrame(frame)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ErrorCode != 45000081 || got.ErrorMessage != string(msg) {
		t.Fatalf("unexpected error frame: %+v", got)
	}
}

func TestDecodeRejectsShortFrame(t *testing.T) {
	if _, err := decodeFrame([]byte{0x11, 0x10}); err == nil {
		t.Fatal("expected error")
	}
}

func TestASRRequestJSONUsesPCM16k(t *testing.T) {
	req := asrRequest{
		Audio: asrAudio{Format: "pcm", Rate: 16000, Bits: 16, Channel: 1, Language: "zh-CN"},
		Request: asrRequestOptions{ModelName: "bigmodel", EnableITN: true, EnablePunc: true,
			ShowUtterances: true, ResultType: "full", EnableNonstream: true, EndWindowSize: 800},
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	audio := got["audio"].(map[string]any)
	if audio["format"] != "pcm" || audio["rate"].(float64) != 16000 || audio["bits"].(float64) != 16 {
		t.Fatalf("unexpected audio config: %v", audio)
	}
}
