package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

const testBugUID = "bug_11111111-1111-4111-8111-111111111111"

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
		case "/api/v1/tool/bugs/" + testBugUID + "/screenshot":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"url": files.URL + "/shot", "expires": 1}})
		case "/api/v1/tool/bugs/" + testBugUID + "/log":
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
	body, err := readBugLog(c, testBugUID)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "0123456789abcdef" {
		t.Fatalf("log %q", body)
	}
	dest := t.TempDir() + "/shot.bin"
	if err := saveBugScreenshot(c, testBugUID, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "PNGDATA" {
		t.Fatalf("screenshot %q %v", got, err)
	}
}

func TestPullRejectsTraversalUID(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	dir := t.TempDir()
	for _, uid := range []string{"", "../outside", `..\outside`, "bug_not-a-uuid", testBugUID + "/../../etc/passwd"} {
		err := pullBugFiles(testClient(srv.URL), dir, uid)
		if err == nil || exitCode(err) != exitUsage {
			t.Fatalf("uid %q: %v", uid, err)
		}
	}
	if called {
		t.Fatal("request sent before the bug uid was checked")
	}
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(dir), "outside*"))
	if len(matches) != 0 {
		t.Fatalf("wrote outside --dir: %v", matches)
	}
}

func TestPullWritesPrivateFiles(t *testing.T) {
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Secret-Key") != "" {
			t.Errorf("deploy key reached the file host")
		}
		_, _ = w.Write([]byte("PNGDATA"))
	}))
	defer files.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/tool/bugs/" + testBugUID + "/screenshot":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"url": files.URL + "/shot"}})
		case "/tool/bugs/" + testBugUID + "/log":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"text": "player log"}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer api.Close()
	dir := filepath.Join(t.TempDir(), "bugs")
	if err := pullBugFiles(testClient(api.URL), dir, testBugUID); err != nil {
		t.Fatal(err)
	}
	shot, err := os.ReadFile(filepath.Join(dir, testBugUID+"-screenshot"))
	if err != nil || string(shot) != "PNGDATA" {
		t.Fatalf("screenshot %q %v", shot, err)
	}
	logBody, err := os.ReadFile(filepath.Join(dir, testBugUID+"-log.txt"))
	if err != nil || string(logBody) != "player log" {
		t.Fatalf("log %q %v", logBody, err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", info)
	}
	for _, name := range []string{testBugUID + "-screenshot", testBugUID + "-log.txt"} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", name, fi)
		}
	}
}
