package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestBugListScreenshotAndLog(t *testing.T) {
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Secret-Key") != "" || r.Header.Get("X-Access-Token") != "" {
			t.Errorf("deploy key reached the file host")
		}
		_, _ = w.Write([]byte("PNGDATA"))
	}))
	defer files.Close()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Secret-Key") != testSecret {
			t.Errorf("API call missing the deploy key on %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/tool/bugs":
			data := map[string]any{"bugs": []any{map[string]any{"uid": "bug_a", "status": "open", "message": "one"}}}
			if r.URL.Query().Get("page") == "1" {
				data["next_page"] = 2
			} else {
				data["bugs"] = []any{map[string]any{"uid": "bug_b", "status": "closed", "message": "two"}}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
		case "/api/v1/tool/bugs/bug_a/screenshot":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"url": files.URL + "/shot", "expires": 1}})
		case "/api/v1/tool/bugs/bug_a/log":
			text, next := "01234567", any(8)
			if r.URL.Query().Get("offset") == "8" {
				text, next = "89abcdef", nil
			}
			data := map[string]any{"text": text, "offset": 0, "total_bytes": 16}
			if next != nil {
				data["next_offset"] = next
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer api.Close()

	c := NewClient(api.URL+"/api/v1", "tok", testSecret)
	bugs, err := listBugTickets(c, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(bugs) != 2 || bugs[1]["status"] != "closed" {
		t.Fatalf("pages: %#v", bugs)
	}
	body, err := readBugLog(c, "bug_a")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "0123456789abcdef" {
		t.Fatalf("log %q", body)
	}
	dest := t.TempDir() + "/shot.bin"
	if err := saveBugScreenshot(c, "bug_a", dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "PNGDATA" {
		t.Fatalf("screenshot %q %v", got, err)
	}
}
