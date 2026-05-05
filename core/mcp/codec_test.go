package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestRequestIDRoundtripString(t *testing.T) {
	id := NewStringRequestID("abc")
	out, err := json.Marshal(id)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != `"abc"` {
		t.Fatalf("unexpected marshal: %s", out)
	}
	var got RequestID
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.IsString() || got.String() != "abc" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}

func TestRequestIDRoundtripInt(t *testing.T) {
	id := NewIntRequestID(42)
	out, err := json.Marshal(id)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != "42" {
		t.Fatalf("unexpected marshal: %s", out)
	}
	var got RequestID
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.IsInt() || got.String() != "42" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}

func TestRequestIDRejectsNull(t *testing.T) {
	var id RequestID
	if err := json.Unmarshal([]byte("null"), &id); err == nil {
		t.Fatalf("null id should be rejected")
	}
}

func TestRequestIDRejectsBoolean(t *testing.T) {
	var id RequestID
	if err := json.Unmarshal([]byte("true"), &id); err == nil {
		t.Fatalf("boolean id should be rejected")
	}
}

func TestFrameWriterFrameReader_RoundtripRequest(t *testing.T) {
	var buf bytes.Buffer
	w := NewFrameWriter(&buf)
	req, err := NewRequest(NewIntRequestID(1), MethodInitialize, InitializeParams{
		ProtocolVersion: ProtocolVersionLatest,
	})
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if err := w.Write(req); err != nil {
		t.Fatalf("write: %v", err)
	}
	r := NewFrameReader(&buf)
	got, err := r.Read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Method != MethodInitialize {
		t.Fatalf("unexpected method: %s", got.Method)
	}
	if !got.ID.IsInt() || got.ID.String() != "1" {
		t.Fatalf("unexpected id: %+v", got.ID)
	}
}

func TestFrameReader_Notification(t *testing.T) {
	var buf bytes.Buffer
	notif, err := NewNotification(MethodInitialized, struct{}{})
	if err != nil {
		t.Fatalf("notif: %v", err)
	}
	w := NewFrameWriter(&buf)
	if err := w.Write(notif); err != nil {
		t.Fatalf("write notif: %v", err)
	}
	r := NewFrameReader(&buf)
	got, err := r.Read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.ID != nil {
		t.Fatalf("notification must not carry id")
	}
}

func TestFrameReader_RejectsHybridFrame(t *testing.T) {
	bad := []byte(`{"jsonrpc":"2.0","id":1,"method":"x","result":{}}` + "\n")
	r := NewFrameReader(bytes.NewReader(bad))
	if _, err := r.Read(); err == nil {
		t.Fatalf("hybrid frame should be rejected")
	}
}

func TestFrameReader_EOF(t *testing.T) {
	r := NewFrameReader(strings.NewReader(""))
	if _, err := r.Read(); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}
