package main

import (
	"archive/zip"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(path), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestZipKeepsRelativePaths(t *testing.T) {
	dir := t.TempDir()
	files := []string{
		filepath.Join(dir, "game", "bin", "game.exe"),
		filepath.Join(dir, "game", "data", "game.pck"),
		filepath.Join(dir, "game", "data", "sub", "readme.txt"),
	}
	for _, f := range files {
		writeFile(t, f)
	}
	out := filepath.Join(dir, "out.zip")
	if err := CreateZip(out, files); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	want := []string{"bin/game.exe", "data/game.pck", "data/sub/readme.txt"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("names = %v, want %v", names, want)
	}
}

func TestZipSingleFileUsesBaseName(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a", "only.bin")
	writeFile(t, f)
	names, err := zipEntryNames([]string{f})
	if err != nil || len(names) != 1 || names[0] != "only.bin" {
		t.Fatalf("names = %v, err = %v", names, err)
	}
}

func TestZipRejectsCollidingNames(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "x", "Game.pck")
	b := filepath.Join(dir, "x", "game.pck")
	if _, err := zipEntryNames([]string{a, b}); err == nil {
		t.Fatal("case-colliding names must be rejected")
	}
	if _, err := zipEntryNames([]string{a, a}); err == nil {
		t.Fatal("duplicate files must be rejected")
	}
}

func TestRedirectDropsCredentialsAcrossHosts(t *testing.T) {
	mk := func(raw string) *http.Request {
		u, _ := url.Parse(raw)
		r := &http.Request{URL: u, Header: http.Header{}}
		for _, h := range credentialHeaders {
			r.Header.Set(h, "secret")
		}
		return r
	}
	first := mk("https://api.blazium.online/api/v1/tool/upload/build")
	same := mk("https://api.blazium.online/api/v1/other")
	if err := checkRedirect(same, []*http.Request{first}); err != nil || same.Header.Get("X-Secret-Key") == "" {
		t.Fatalf("same host must keep headers: err=%v", err)
	}
	other := mk("https://evil.example/collect")
	if err := checkRedirect(other, []*http.Request{first}); err != nil {
		t.Fatal(err)
	}
	for _, h := range credentialHeaders {
		if other.Header.Get(h) != "" {
			t.Fatalf("%s must be dropped on a cross-host redirect", h)
		}
	}
	if err := checkRedirect(mk("http://api.blazium.online/x"), []*http.Request{first}); err == nil {
		t.Fatal("https to http redirect must be refused")
	}
	via := make([]*http.Request, 10)
	for i := range via {
		via[i] = first
	}
	if err := checkRedirect(same, via); err == nil {
		t.Fatal("redirect loops must stop")
	}
}

func TestReadSecret(t *testing.T) {
	for in, want := range map[string]string{"abc\n": "abc", "  abc  \r\nrest": "abc", "abc": "abc", "": ""} {
		got, err := readSecret(strings.NewReader(in))
		if err != nil || got != want {
			t.Fatalf("readSecret(%q) = %q, %v", in, got, err)
		}
	}
}

func TestHTTPClientHasTimeouts(t *testing.T) {
	c := newHTTPClient()
	tr, ok := c.Transport.(*http.Transport)
	if c.Timeout == 0 || !ok || tr.ResponseHeaderTimeout == 0 || tr.TLSHandshakeTimeout == 0 || c.CheckRedirect == nil {
		t.Fatal("client must bound requests and check redirects")
	}
}
