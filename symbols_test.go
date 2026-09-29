package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const symHeader = "MODULE windows x86_64 0123456789ABCDEF0123456789ABCDEF0 game.pdb\nFILE 0 main.cpp\n"

func writeSym(t *testing.T, path, content string) string {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPrepareSymbolsSingleFile(t *testing.T) {
	dir := t.TempDir()
	up, err := prepareSymbols(writeSym(t, filepath.Join(dir, "game.sym"), symHeader))
	if err != nil || up.Count != 1 {
		t.Fatalf("up=%+v err=%v", up, err)
	}
	if _, err := prepareSymbols(writeSym(t, filepath.Join(dir, "bad.sym"), "not a symbol file\n")); err == nil {
		t.Fatal("file without MODULE header accepted")
	}
	if _, err := prepareSymbols(writeSym(t, filepath.Join(dir, "game.pdb"), symHeader)); err == nil {
		t.Fatal("wrong extension accepted")
	}
}

func TestPrepareSymbolsDirectoryIsZipped(t *testing.T) {
	dir := t.TempDir()
	writeSym(t, filepath.Join(dir, "game.pdb", "AAA", "game.sym"), symHeader)
	writeSym(t, filepath.Join(dir, "lib.pdb", "BBB", "lib.sym"), symHeader)
	writeSym(t, filepath.Join(dir, "notes.txt"), "ignored")
	up, err := prepareSymbols(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer up.cleanup()
	if up.Count != 2 || !strings.HasSuffix(up.Path, ".zip") {
		t.Fatalf("up = %+v", up)
	}
	zr, err := zip.OpenReader(up.Path)
	if err != nil {
		t.Fatal(err)
	}
	entries := len(zr.File)
	zr.Close()
	if entries != 2 {
		t.Fatalf("zip has %d entries", entries)
	}
	tmp := up.Path
	up.cleanup()
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatal("temporary zip was not removed")
	}
	if _, err := prepareSymbols(t.TempDir()); err == nil {
		t.Fatal("empty directory accepted")
	}
}

func TestPrepareSymbolsZipNeedsSymFiles(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.zip")
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("readme.txt")
	w.Write([]byte("x"))
	zw.Close()
	f.Close()
	if _, err := prepareSymbols(p); err == nil {
		t.Fatal("zip without .sym files accepted")
	}
}

func TestUploadSymbolsSendsBuildAndChecksum(t *testing.T) {
	sym := writeSym(t, filepath.Join(t.TempDir(), "game.sym"), symHeader)
	sum := sha256.Sum256([]byte(symHeader))
	var order []string
	fields := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tool/upload/symbols" {
			http.NotFound(w, r)
			return
		}
		mr, _ := r.MultipartReader()
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			order = append(order, p.FormName())
			b, _ := io.ReadAll(p)
			fields[p.FormName()] = string(b)
			if p.FileName() != "" {
				fields["filename"] = p.FileName()
			}
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"build_id": fields["build_id"], "count": 1}})
	}))
	defer srv.Close()
	const build = "5f1c2d3e-0000-4000-8000-000000000000"
	data, err := uploadSymbols(testClient(srv.URL), build, sym)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "build_id,checksum,file" {
		t.Fatalf("order = %v", order)
	}
	if fields["build_id"] != build || fields["checksum"] != hex.EncodeToString(sum[:]) || fields["filename"] != "game.sym" {
		t.Fatalf("fields = %v", fields)
	}
	if int64Of(data["count"]) != 1 {
		t.Fatalf("data = %v", data)
	}
	if _, err := uploadSymbols(testClient(srv.URL), "not-a-uid", sym); exitCode(err) != exitUsage {
		t.Fatalf("bad build id: %v", err)
	}
}
