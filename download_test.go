package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadFileKeepsDeployKeyOnAPIHostOnly(t *testing.T) {
	var otherSaw string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherSaw = r.Header.Get("X-Secret-Key") + r.Header.Get("X-Access-Token")
		_, _ = w.Write([]byte("other"))
	}))
	defer other.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			if r.Header.Get("X-Secret-Key") == "" {
				t.Errorf("API host missing the deploy key")
			}
			http.Redirect(w, r, other.URL+"/file", http.StatusFound)
			return
		}
		if r.Header.Get("X-Secret-Key") == "" || r.Header.Get("X-Access-Token") == "" {
			t.Errorf("API host missing the deploy key")
		}
		_, _ = w.Write([]byte("direct"))
	}))
	defer api.Close()

	c := NewClient(api.URL, "tok", "sekrit")
	if err := c.DownloadFile(api.URL+"/direct", filepath.Join(t.TempDir(), "direct")); err != nil {
		t.Fatal(err)
	}
	if err := c.DownloadFile(other.URL+"/file", filepath.Join(t.TempDir(), "other")); err != nil {
		t.Fatal(err)
	}
	if otherSaw != "" {
		t.Fatalf("deploy key sent to a different host: %q", otherSaw)
	}
	otherSaw = "unset"
	if err := c.DownloadFile(api.URL+"/redirect", filepath.Join(t.TempDir(), "redir")); err != nil {
		t.Fatal(err)
	}
	if otherSaw != "" {
		t.Fatalf("deploy key followed a cross-host redirect: %q", otherSaw)
	}
}

func TestDownloadFileRejectsPlainHTTP(t *testing.T) {
	c := NewClient("https://api.blazium.online", "tok", "sekrit")
	err := c.DownloadFile("http://cdn.example/file", filepath.Join(t.TempDir(), "x"))
	if err == nil || exitCode(err) != exitUsage {
		t.Fatalf("http download: %v", err)
	}
}

func TestDownloadFileRejectsOversize(t *testing.T) {
	old := maxDownloadBytes
	maxDownloadBytes = 8
	t.Cleanup(func() { maxDownloadBytes = old })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 9))
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "out")
	c := NewClient("https://api.blazium.online", "tok", "sekrit")
	if err := c.DownloadFile(srv.URL+"/f", dest); err == nil {
		t.Fatal("oversize download succeeded")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("partial download was left on disk")
	}
}
