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
	if err := checkImage(writePNG(t, dir, "shot.png", 1280, 720), slotScreenshot); err != nil {
		t.Fatalf("valid screenshot rejected: %v", err)
	}
	if err := checkImage(writePNG(t, dir, "cover.png", 1024, 576), slotCover); err != nil {
		t.Fatalf("valid cover rejected: %v", err)
	}
	if err := checkImage(writePNG(t, dir, "avatar.png", 256, 256), slotAvatar); err != nil {
		t.Fatalf("valid avatar rejected: %v", err)
	}
	if err := checkImage(writePNG(t, dir, "small.png", 959, 540), slotThumbnail); err == nil || !strings.Contains(err.Error(), "minimum is 960x540") {
		t.Fatalf("small thumbnail: %v", err)
	}
	if err := checkImage(writePNG(t, dir, "big.png", 1921, 1080), slotThumbnail); err == nil || !strings.Contains(err.Error(), "maximum is 1920x1080") {
		t.Fatalf("oversized thumbnail: %v", err)
	}
	if err := checkImage(writePNG(t, dir, "wide.png", 1600, 1000), slotThumbnail); err == nil || !strings.Contains(err.Error(), "16:9") {
		t.Fatalf("wrong aspect: %v", err)
	}
	if err := checkImage(writePNG(t, dir, "tall.png", 400, 300), slotAvatar); err == nil || !strings.Contains(err.Error(), "square") {
		t.Fatalf("avatar aspect: %v", err)
	}
	fake := filepath.Join(dir, "fake.png")
	os.WriteFile(fake, []byte("<html>not an image</html>"), 0o644)
	if err := checkImage(fake, slotScreenshot); err == nil || exitCode(err) != exitUsage {
		t.Fatalf("non-image with .png extension: %v", err)
	}
	if err := checkImage(dir, slotScreenshot); err == nil {
		t.Fatal("directory accepted as image")
	}
}

func TestBuildImagesUseEachSlot(t *testing.T) {
	dir := t.TempDir()
	cover := writePNG(t, dir, "cover.png", 1024, 576)
	thumb := writePNG(t, dir, "thumb.png", 960, 540)
	shot := writePNG(t, dir, "shot.png", 1280, 720)
	legacy := writePNG(t, dir, "legacy.png", 960, 540)
	got := buildImages(&MediaSpec{Cover: cover, Thumbnail: thumb, Gallery: []string{shot}}, []string{legacy})
	want := []slotImage{{cover, slotCover}, {thumb, slotThumbnail}, {shot, slotScreenshot}, {legacy, slotScreenshot}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i].path != want[i].path || got[i].slot.name != want[i].slot.name {
			t.Fatalf("image %d: got %s as %s, want %s", i, got[i].path, got[i].slot.name, want[i].slot.name)
		}
	}
	// A minimum-size cover and thumbnail pass; the same thumbnail in the gallery is too small for a screenshot.
	for _, img := range got[:3] {
		if err := checkImage(img.path, img.slot); err != nil {
			t.Fatalf("%s as %s: %v", img.path, img.slot.name, err)
		}
	}
	if err := checkImage(got[3].path, got[3].slot); err == nil || !strings.Contains(err.Error(), "minimum is 1280x720") {
		t.Fatalf("legacy gallery image: %v", err)
	}
	if buildImages(nil, nil) != nil {
		t.Fatal("no media means no images")
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
	imgs := []string{writePNG(t, dir, "1.png", 1280, 720), writePNG(t, dir, "2.png", 1920, 1080)}
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
