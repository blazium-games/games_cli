package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeUploader mimics games_upload's single-shot and session endpoints.
type fakeUploader struct {
	mu         sync.Mutex
	session    map[string]string
	data       []byte
	partOrder  []string
	fail503    int // fail this many chunk requests with 503
	loseOnce   bool
	chunkCalls int
}

func writeEnvelope(w http.ResponseWriter, status int, data map[string]any, code int, msg string, errData map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]any{"success": status < 300}
	if status < 300 {
		body["data"] = data
	} else {
		body["error"] = map[string]any{"code": code, "message": msg, "data": errData}
	}
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeUploader) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("X-Access-Token") != "tok" || r.Header.Get("X-Secret-Key") != "sekrit" {
		writeEnvelope(w, 401, nil, 4025, "Asset authentication required", nil)
		return
	}
	switch {
	case r.URL.Path == "/tool/upload/sessions":
		_ = r.ParseForm()
		f.session = map[string]string{}
		for _, k := range []string{"filename", "checksum", "os", "arch", "channel", "build_id", "total_size"} {
			f.session[k] = r.PostForm.Get(k)
		}
		writeEnvelope(w, 201, map[string]any{"session_id": "sess-1", "current_size": 0}, 0, "", nil)
	case r.URL.Path == "/tool/upload/files" && r.Header.Get("X-Upload-Session-ID") == "":
		mr, err := r.MultipartReader()
		if err != nil {
			writeEnvelope(w, 400, nil, 4037, "bad body", nil)
			return
		}
		fields := map[string]string{}
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			f.partOrder = append(f.partOrder, p.FormName())
			b, _ := io.ReadAll(p)
			if p.FileName() != "" {
				f.data = b
			} else {
				fields[p.FormName()] = string(b)
			}
		}
		sum := sha256.Sum256(f.data)
		if fields["checksum"] != hex.EncodeToString(sum[:]) {
			writeEnvelope(w, 400, nil, 4046, "Checksum mismatch", nil)
			return
		}
		writeEnvelope(w, 201, map[string]any{"file_uid": "file-1", "status": "processing"}, 0, "", nil)
	case r.URL.Path == "/tool/upload/files":
		f.chunkCalls++
		if f.fail503 > 0 {
			f.fail503--
			writeEnvelope(w, 503, nil, 5000, "busy", nil)
			return
		}
		var start, end, total int64
		if _, err := fmt.Sscanf(r.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total); err != nil {
			writeEnvelope(w, 400, nil, 4044, "bad range", nil)
			return
		}
		if start != int64(len(f.data)) {
			writeEnvelope(w, 409, nil, 4044, "range mismatch", map[string]any{"current_size": len(f.data)})
			return
		}
		mr, err := r.MultipartReader()
		if err != nil {
			writeEnvelope(w, 400, nil, 4037, "bad body", nil)
			return
		}
		p, err := mr.NextPart()
		if err != nil || p.FormName() != "file" {
			writeEnvelope(w, 400, nil, 4037, "no file", nil)
			return
		}
		b, _ := io.ReadAll(p)
		if int64(len(b)) != end-start+1 {
			writeEnvelope(w, 400, nil, 4044, "short chunk", nil)
			return
		}
		f.data = append(f.data, b...)
		acked := len(f.data)
		if f.loseOnce && len(f.data) > len(b) {
			// Acknowledge the chunk, then lose it, so the next chunk gets a 409.
			f.loseOnce = false
			f.data = f.data[:len(f.data)-len(b)]
		}
		if int64(acked) < total {
			writeEnvelope(w, 202, map[string]any{"session_id": "sess-1", "current_size": acked}, 0, "", nil)
			return
		}
		sum := sha256.Sum256(f.data)
		if f.session["checksum"] != hex.EncodeToString(sum[:]) {
			writeEnvelope(w, 400, nil, 4046, "Checksum mismatch", nil)
			return
		}
		writeEnvelope(w, 201, map[string]any{"file_uid": "file-2", "status": "processing"}, 0, "", nil)
	default:
		http.NotFound(w, r)
	}
}

func randomFile(t *testing.T, size int) (string, string) {
	t.Helper()
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "game.zip")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return p, hex.EncodeToString(sum[:])
}

func shrinkUploads(t *testing.T, single, chunk int64) {
	t.Helper()
	oldS, oldC, oldB := singleShotMaxBytes, chunkBytes, backoff
	singleShotMaxBytes, chunkBytes = single, chunk
	backoff = func(int) time.Duration { return time.Millisecond }
	t.Cleanup(func() { singleShotMaxBytes, chunkBytes, backoff = oldS, oldC, oldB })
}

func testClient(url string) *Client {
	c := NewClient(url, "tok", "sekrit")
	c.SetUploadURL(url)
	return c
}

var testPlatform = PlatformSpec{OS: "windows", Arch: "x86_64", Channel: "stable"}

func TestSingleShotUploadSendsFieldsBeforeFile(t *testing.T) {
	shrinkUploads(t, 1<<20, 64<<10)
	fu := &fakeUploader{}
	srv := httptest.NewServer(fu)
	defer srv.Close()
	path, sum := randomFile(t, 100_000)
	data, err := uploadBuildFile(testClient(srv.URL), buildFileUpload{Path: path, Size: 100_000, Checksum: sum, BuildID: "b1", Platform: testPlatform})
	if err != nil {
		t.Fatal(err)
	}
	if data["file_uid"] != "file-1" {
		t.Fatalf("data = %v", data)
	}
	if got := strings.Join(fu.partOrder, ","); got != "build_id,os,arch,channel,checksum,file" {
		t.Fatalf("part order = %s", got)
	}
}

func TestChunkedUploadResumesAndRetries(t *testing.T) {
	shrinkUploads(t, 1<<10, 64<<10)
	fu := &fakeUploader{fail503: 2, loseOnce: true}
	srv := httptest.NewServer(fu)
	defer srv.Close()
	const size = 300_000
	path, sum := randomFile(t, size)
	data, err := uploadBuildFile(testClient(srv.URL), buildFileUpload{Path: path, Size: size, Checksum: sum, BuildID: "b1", Platform: testPlatform})
	if err != nil {
		t.Fatal(err)
	}
	if data["file_uid"] != "file-2" {
		t.Fatalf("data = %v", data)
	}
	if len(fu.data) != size {
		t.Fatalf("server has %d bytes, want %d", len(fu.data), size)
	}
	if fu.session["total_size"] != "300000" || fu.session["os"] != "windows" || fu.session["filename"] != "game.zip" {
		t.Fatalf("session fields = %v", fu.session)
	}
	// 5 chunks, 2 retried 503s, 1 resend after the lost chunk, 1 rejected 409.
	if fu.chunkCalls < 8 {
		t.Fatalf("chunk calls = %d; retries or resume did not happen", fu.chunkCalls)
	}
}

func TestChunkedUploadGivesUpAfterMaxTries(t *testing.T) {
	shrinkUploads(t, 1<<10, 64<<10)
	fu := &fakeUploader{fail503: 100}
	srv := httptest.NewServer(fu)
	defer srv.Close()
	path, sum := randomFile(t, 100_000)
	_, err := uploadBuildFile(testClient(srv.URL), buildFileUpload{Path: path, Size: 100_000, Checksum: sum, BuildID: "b1", Platform: testPlatform})
	if err == nil || exitCode(err) != exitAPI {
		t.Fatalf("err = %v, exit %d", err, exitCode(err))
	}
	if fu.chunkCalls != maxChunkTries {
		t.Fatalf("chunk calls = %d, want %d", fu.chunkCalls, maxChunkTries)
	}
}

func TestChecksumMismatchIsNotRetried(t *testing.T) {
	shrinkUploads(t, 1<<20, 64<<10)
	fu := &fakeUploader{}
	srv := httptest.NewServer(fu)
	defer srv.Close()
	path, _ := randomFile(t, 10_000)
	_, err := uploadBuildFile(testClient(srv.URL), buildFileUpload{Path: path, Size: 10_000, Checksum: strings.Repeat("0", 64), BuildID: "b1", Platform: testPlatform})
	if err == nil || !strings.Contains(err.Error(), "4046") || !strings.Contains(err.Error(), "hint") {
		t.Fatalf("err = %v", err)
	}
}

func TestUploadRejectsOversizeLocally(t *testing.T) {
	_, err := uploadBuildFile(testClient("https://example.invalid"), buildFileUpload{Path: "x.zip", Size: maxBuildFileBytes + 1})
	if exitCode(err) != exitUsage {
		t.Fatalf("err = %v", err)
	}
}
