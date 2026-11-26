//go:build unix

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// These tests fail while saving a bug screenshot or log follows a symlink
// that already exists at the destination. The bytes are the report body.
// The destination name is the one pull derives, or the path passed to the
// screenshot and log commands.

const (
	originalFileBytes = "original-file-bytes"
	playerShotBytes   = "player-screenshot"
	playerLogBytes    = "player-log-line"
)

func newBugDownloadFixture(t *testing.T, includeShot bool) *Client {
	t.Helper()
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(playerShotBytes))
	}))
	t.Cleanup(files.Close)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/tool/bugs/" + testBugUID + "/screenshot":
			if !includeShot {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"success": false,
					"error":   map[string]any{"code": 4040, "message": "missing"},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"url": files.URL + "/shot"},
			})
		case "/tool/bugs/" + testBugUID + "/log":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"text": playerLogBytes},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(api.Close)
	return testClient(api.URL)
}

// outsideSymlink plants name inside project/bugs as a relative symlink to a
// file two directories above that directory.
func outsideSymlink(t *testing.T, name string) (dir, link, target, rel string) {
	t.Helper()
	root := t.TempDir()
	target = filepath.Join(root, "original-file")
	if err := os.WriteFile(target, []byte(originalFileBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(root, "project", "bugs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	rel = filepath.Join("..", "..", "original-file")
	link = filepath.Join(dir, name)
	if err := os.Symlink(rel, link); err != nil {
		t.Fatal(err)
	}
	return dir, link, target, rel
}

func requireRefusedSymlink(t *testing.T, err error, link, rel, target string) {
	t.Helper()
	if err == nil {
		t.Fatal("write through an existing symlink succeeded")
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil || string(got) != originalFileBytes {
		t.Fatalf("symlink target = %q, %v", got, readErr)
	}
	st, lerr := os.Lstat(link)
	if lerr != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("destination symlink was removed: mode=%v err=%v", st, lerr)
	}
	gotRel, rerr := os.Readlink(link)
	if rerr != nil || gotRel != rel {
		t.Fatalf("Readlink = %q, %v; want %q", gotRel, rerr, rel)
	}
}

func TestPullScreenshotSymlinkPreserved(t *testing.T) {
	dir, link, target, rel := outsideSymlink(t, testBugUID+"-screenshot")
	err := pullBugFiles(newBugDownloadFixture(t, true), dir, testBugUID)
	requireRefusedSymlink(t, err, link, rel, target)
}

func TestPullLogSymlinkPreserved(t *testing.T) {
	dir, link, target, rel := outsideSymlink(t, testBugUID+"-log.txt")
	// No screenshot, so the log write is the one that can follow the symlink.
	err := pullBugFiles(newBugDownloadFixture(t, false), dir, testBugUID)
	requireRefusedSymlink(t, err, link, rel, target)
}

func TestScreenshotOutSymlinkPreserved(t *testing.T) {
	_, link, target, rel := outsideSymlink(t, testBugUID+"-screenshot")
	err := saveBugScreenshot(newBugDownloadFixture(t, true), testBugUID, link)
	requireRefusedSymlink(t, err, link, rel, target)
}

func TestLogOutSymlinkPreserved(t *testing.T) {
	_, link, target, rel := outsideSymlink(t, testBugUID+"-log.txt")
	c := newBugDownloadFixture(t, false)
	body, err := readBugLog(c, testBugUID)
	if err != nil {
		t.Fatal(err)
	}
	err = writePrivateFile(link, body)
	requireRefusedSymlink(t, err, link, rel, target)
}

func TestDownloadFileSymlinkPreserved(t *testing.T) {
	_, link, target, rel := outsideSymlink(t, "shot.bin")
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(playerShotBytes))
	}))
	t.Cleanup(files.Close)
	err := testClient("https://api.example").DownloadFile(files.URL+"/shot", link)
	requireRefusedSymlink(t, err, link, rel, target)
}

func TestWritePrivateFileSymlinkPreserved(t *testing.T) {
	_, link, target, rel := outsideSymlink(t, testBugUID+"-log.txt")
	err := writePrivateFile(link, []byte(playerLogBytes))
	requireRefusedSymlink(t, err, link, rel, target)
}
