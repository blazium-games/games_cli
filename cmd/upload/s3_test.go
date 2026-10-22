package main

import (
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultACLIsNotPublic(t *testing.T) {
	if defaultACL == "public-read" || defaultACL != "private" {
		t.Fatalf("default acl %q", defaultACL)
	}
	fs := flag.NewFlagSet("upload", flag.ContinueOnError)
	acl := fs.String("acl", defaultACL, "")
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if *acl != "private" {
		t.Fatalf("parsed acl %q", *acl)
	}
}

func TestUploadACLRefusesPublicPrivatePath(t *testing.T) {
	if err := checkUploadACL("private/builds/", "public-read"); err == nil {
		t.Fatal("public-read of a private path accepted")
	}
	if err := checkUploadACL("tools/chauffeur/linux-amd64/1.2.3/archive/", "public-read"); err != nil {
		t.Fatal(err)
	}
	if err := checkUploadACL("private/builds/", "private"); err != nil {
		t.Fatal(err)
	}
}

func TestEscapeS3Key(t *testing.T) {
	got := escapeS3Path("/releases/my file?.zip")
	if got != "/releases/my%20file%3F.zip" {
		t.Fatalf("escaped key %q", got)
	}
	if strings.Contains(got, " ") || strings.Contains(got, "?") {
		t.Fatalf("raw reserved character in %q", got)
	}
}

func TestPutS3StreamsEscapedKey(t *testing.T) {
	body := []byte("zip-bytes")
	var gotPath, gotACL string
	var gotLen int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		gotACL = r.Header.Get("x-amz-acl")
		b, _ := io.ReadAll(r.Body)
		gotLen = len(b)
		if r.ContentLength != int64(len(body)) || string(b) != string(body) {
			t.Errorf("body len %d content-length %d", len(b), r.ContentLength)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	old := spacesEndpoint
	spacesEndpoint = srv.URL
	t.Cleanup(func() { spacesEndpoint = old })

	dir := t.TempDir()
	name := "my file.bin"
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CDN_SPACE", "space")
	t.Setenv("CDN_REGION", "nyc3")
	t.Setenv("CDN_KEY", "key")
	t.Setenv("CDN_SECRET", "secret")
	if err := putS3(dir, name, "/releases/", "private", "STANDARD"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotPath, "my%20file.bin") {
		t.Fatalf("request path %q", gotPath)
	}
	if gotACL != "private" {
		t.Fatalf("acl %q", gotACL)
	}
	if gotLen != len(body) {
		t.Fatalf("streamed %d bytes", gotLen)
	}
}
