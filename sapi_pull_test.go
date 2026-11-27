package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestZip(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pack.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return path
}

func TestExtractSapiPack(t *testing.T) {
	dir := t.TempDir()
	zp := writeTestZip(t, map[string]string{"daily.luau": "return 1", "lib/router.luau": "return 2"})
	written, err := extractSapiPack(zp, dir, false)
	if err != nil || len(written) != 2 {
		t.Fatalf("%v %v", written, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "lib", "router.luau")); string(b) != "return 2" {
		t.Fatalf("lib/router.luau = %q", b)
	}
	// Same content again is fine; a locally edited file is protected until --force.
	if _, err := extractSapiPack(zp, dir, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "daily.luau"), []byte("local edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := extractSapiPack(zp, dir, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("overwrote a local edit: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "daily.luau")); string(b) != "local edit" {
		t.Fatalf("local edit lost: %q", b)
	}
	if _, err := extractSapiPack(zp, dir, true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "daily.luau")); string(b) != "return 1" {
		t.Fatalf("force did not overwrite: %q", b)
	}
}

func TestExtractSapiPackRejectsBadFiles(t *testing.T) {
	for _, name := range []string{"../evil.luau", "/abs.luau", "readme.md", "a\\b.luau", "C:x.luau"} {
		dir := t.TempDir()
		zp := writeTestZip(t, map[string]string{name: "x"})
		if _, err := extractSapiPack(zp, dir, true); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
}
