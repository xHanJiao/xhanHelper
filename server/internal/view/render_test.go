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
