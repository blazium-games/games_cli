package main

import (
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePNG(t *testing.T, dir, name string, w, h int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckImage(t *testing.T) {
	dir := t.TempDir()
	if err := checkImage(writePNG(t, dir, "ok.png", 512, 1024)); err != nil {
		t.Fatalf("valid image rejected: %v", err)
	}
	if err := checkImage(writePNG(t, dir, "small.png", 256, 600)); err == nil || !strings.Contains(err.Error(), "512-2048") {
		t.Fatalf("small image: %v", err)
	}
	if err := checkImage(writePNG(t, dir, "big.png", 2049, 600)); err == nil {
		t.Fatal("oversized image accepted")
	}
	fake := filepath.Join(dir, "fake.png")
	os.WriteFile(fake, []byte("<html>not an image</html>"), 0o644)
	if err := checkImage(fake); err == nil || exitCode(err) != exitUsage {
		t.Fatalf("non-image with .png extension: %v", err)
	}
	if err := checkImage(dir); err == nil {
		t.Fatal("directory accepted as image")
	}
}

func TestReorderMove(t *testing.T) {
	got, err := reorderMove([]string{"a", "b", "c", "d"}, "d", 1)
	if err != nil || strings.Join(got, "") != "adbc" {
		t.Fatalf("got %v, %v", got, err)
	}
	got, _ = reorderMove([]string{"a", "b", "c"}, "a", 2)
	if strings.Join(got, "") != "bca" {
		t.Fatalf("got %v", got)
	}
	if _, err := reorderMove([]string{"a"}, "z", 0); err == nil {
		t.Fatal("unknown uid accepted")
	}
	if _, err := reorderMove([]string{"a", "b"}, "a", 2); err == nil {
		t.Fatal("out of range index accepted")
	}
}

func TestValidateOrder(t *testing.T) {
	cur := []string{"a", "b", "c"}
	if err := validateOrder([]string{"c", "a", "b"}, cur); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]string{{"a", "b"}, {"a", "a", "b"}, {"a", "b", "z"}} {
		if err := validateOrder(bad, cur); err == nil {
			t.Fatalf("%v accepted", bad)
		}
	}
}

func TestAddMediaSendsKindAndFiles(t *testing.T) {
	dir := t.TempDir()
	imgs := []string{writePNG(t, dir, "1.png", 600, 600), writePNG(t, dir, "2.png", 600, 600)}
	var kind, position string
	var files int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tool/media" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		mr, _ := r.MultipartReader()
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			switch p.FormName() {
			case "kind":
				kind = string(b)
			case "position":
				position = string(b)
			case "file":
				files++
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
			"game_uid": "g", "gallery": []any{map[string]any{"uid": "u1", "url": "https://cdn/x.png", "position": 0}}, "gallery_max": 20,
		}})
	}))
	defer srv.Close()
	m, err := addMedia(testClient(srv.URL), "gallery", imgs, 0)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "gallery" || position != "0" || files != 2 {
		t.Fatalf("kind=%q position=%q files=%d", kind, position, files)
	}
	if len(m.Gallery) != 1 || m.Gallery[0].UID != "u1" {
		t.Fatalf("media = %+v", m)
	}
	if _, err := addMedia(testClient(srv.URL), "cover", imgs, -1); err == nil {
		t.Fatal("cover with two images accepted")
	}
	if _, err := addMedia(testClient(srv.URL), "banner", imgs[:1], -1); err == nil {
		t.Fatal("unknown kind accepted")
	}
}
