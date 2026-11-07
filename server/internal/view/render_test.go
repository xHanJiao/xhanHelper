package view

import (
	"bytes"
	"testing"
)

type nopFlusher struct{}

func (nopFlusher) Flush() {}

func TestWriteSSESplitsMultilineData(t *testing.T) {
	var buf bytes.Buffer
	WriteSSE(&buf, nopFlusher{}, "thought_delta", "第一行\n第二行")
	want := "event: thought_delta\ndata: 第一行\ndata: 第二行\n\n"
	if got := buf.String(); got != want {
		t.Fatalf("WriteSSE multiline:\n got %q\nwant %q", got, want)
	}
}

func TestWriteSSENormalizesCRLF(t *testing.T) {
	var buf bytes.Buffer
	WriteSSE(&buf, nopFlusher{}, "error", "a\r\nb")
	want := "event: error\ndata: a\ndata: b\n\n"
	if got := buf.String(); got != want {
		t.Fatalf("WriteSSE CRLF:\n got %q\nwant %q", got, want)
	}
}

func TestWriteSSESingleLineUnchanged(t *testing.T) {
	var buf bytes.Buffer
	WriteSSE(&buf, nopFlusher{}, "message", `{"delta":"hi"}`)
	want := "event: message\ndata: {\"delta\":\"hi\"}\n\n"
	if got := buf.String(); got != want {
		t.Fatalf("WriteSSE single line:\n got %q\nwant %q", got, want)
	}
}

func TestWriteSSEIDEmitsSequence(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteSSEID(&buf, nopFlusher{}, 7, "message", "hi"); err != nil {
		t.Fatalf("WriteSSEID error: %v", err)
	}
	want := "id: 7\nevent: message\ndata: hi\n\n"
	if got := buf.String(); got != want {
		t.Fatalf("WriteSSEID:\n got %q\nwant %q", got, want)
	}
}

func TestWriteComment(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteComment(&buf, nopFlusher{}, "ping"); err != nil {
		t.Fatalf("WriteComment error: %v", err)
	}
	if got, want := buf.String(), ": ping\n\n"; got != want {
		t.Fatalf("WriteComment:\n got %q\nwant %q", got, want)
	}
}
