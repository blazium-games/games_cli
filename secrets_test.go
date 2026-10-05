package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testSecret = "test-deploy-secret"

func TestErrorsNeverContainTheSecret(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]any{"success": false, "error": map[string]any{"code": 4025, "message": "Asset authentication required"}})
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "tok", testSecret)
	_, err := c.GetJSON("/tool/info")
	if err == nil || strings.Contains(err.Error(), testSecret) {
		t.Fatalf("err = %v", err)
	}

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	c = NewClient("http://"+addr, "tok", testSecret)
	_, err = c.GetJSON("/tool/info")
	if exitCode(err) != exitNetwork || strings.Contains(err.Error(), testSecret) {
		t.Fatalf("network err = %v", err)
	}
}

func TestScrubErrorRedactsCredentials(t *testing.T) {
	err := scrubError(errors.New("proxy said: bad header X-Secret-Key="+testSecret), testSecret, "tok")
	if strings.Contains(err.Error(), testSecret) || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("err = %v", err)
	}
}

func TestJSONErrorOutputHasNoSecret(t *testing.T) {
	var buf bytes.Buffer
	oldOut, oldJSON := stdout, jsonOutput
	stdout, jsonOutput = &buf, true
	defer func() { stdout, jsonOutput = oldOut, oldJSON }()
	emitError(&apiError{Status: 401, Code: 4025, Message: "Asset authentication required"})
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != false || out["code"].(float64) != 4025 || out["hint"] == "" || out["exit_code"].(float64) != exitAPI {
		t.Fatalf("out = %v", out)
	}
	if strings.Contains(buf.String(), testSecret) {
		t.Fatal("secret in JSON output")
	}
}

func TestRedactURLDropsUserAndQuery(t *testing.T) {
	got := redactURL("https://user:pw@api.example/x?token=abc")
	if strings.Contains(got, "pw") || strings.Contains(got, "abc") {
		t.Fatalf("got %s", got)
	}
}
