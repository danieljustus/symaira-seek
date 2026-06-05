package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWriteJSONRPCFrame_ValidEnvelope(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      1,
		Result:  map[string]string{"ok": "true"},
	}
	writeJSONRPCFrame(stdout, stderr, resp)

	if stderr.Len() != 0 {
		t.Errorf("expected no stderr output on happy path, got %q", stderr.String())
	}
	out := strings.TrimSuffix(stdout.String(), "\n")
	var got JSONRPCResponse
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("writeJSONRPCFrame produced invalid JSON: %v\nout=%q", err, out)
	}
	if got.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc=2.0, got %q", got.JSONRPC)
	}
	if !strings.HasSuffix(stdout.String(), "\n") {
		t.Errorf("expected trailing newline, got %q", stdout.String())
	}
}

func TestWriteJSONRPCFrame_ErrorEnvelope(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      "abc",
		Error: map[string]interface{}{
			"code":    -32600,
			"message": "invalid request",
		},
	}
	writeJSONRPCFrame(stdout, stderr, resp)

	out := strings.TrimSuffix(stdout.String(), "\n")
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("writeJSONRPCFrame produced invalid JSON: %v\nout=%q", err, out)
	}
	if got["id"] != "abc" {
		t.Errorf("expected id=abc, got %v", got["id"])
	}
	errObj, ok := got["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected error object, got %T", got["error"])
	}
	if errObj["code"].(float64) != -32600 {
		t.Errorf("expected code -32600, got %v", errObj["code"])
	}
	if errObj["message"] != "invalid request" {
		t.Errorf("expected message 'invalid request', got %v", errObj["message"])
	}
}

func TestWriteJSONRPCFrame_FallbackOnMarshalFailure(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      1,
		Result:  make(chan int),
	}
	writeJSONRPCFrame(stdout, stderr, resp)

	if !strings.Contains(stderr.String(), "failed to marshal") {
		t.Errorf("expected marshal error to be logged to stderr, got %q", stderr.String())
	}

	out := strings.TrimSuffix(stdout.String(), "\n")
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("fallback frame is not valid JSON: %v\nout=%q", err, out)
	}
	if got["jsonrpc"] != "2.0" {
		t.Errorf("expected jsonrpc=2.0 in fallback, got %v", got["jsonrpc"])
	}
	if got["id"] != nil {
		t.Errorf("expected id=null in fallback, got %v", got["id"])
	}
	errObj, ok := got["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected error object in fallback, got %T", got["error"])
	}
	if errObj["code"].(float64) != -32603 {
		t.Errorf("expected fallback code -32603, got %v", errObj["code"])
	}
	if !strings.Contains(errObj["message"].(string), "marshal") {
		t.Errorf("expected fallback message to mention marshal, got %v", errObj["message"])
	}
}
