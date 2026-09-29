package main

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMultipartBodyStreamsWithExactLength(t *testing.T) {
	const size = 256 << 20
	p := filepath.Join(t.TempDir(), "big.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	f.Close()

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	body, length, _, done, err := multipartBody([]formField{{"build_id", "b1"}}, []formFile{{Field: "file", Path: p, Length: -1}})
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(io.Discard, body)
	done()
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if n != length {
		t.Fatalf("body is %d bytes, Content-Length says %d", n, length)
	}
	if n < size {
		t.Fatalf("body is %d bytes, smaller than the file", n)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 8<<20 {
		t.Fatalf("streaming a 256 MB file allocated %d bytes; the body must not be buffered", grew)
	}
}

func TestMultipartBodySection(t *testing.T) {
	p := filepath.Join(t.TempDir(), "data.bin")
	if err := os.WriteFile(p, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, length, ctype, done, err := multipartBody(
		[]formField{{"a", "1"}, {"b", "2"}},
		[]formFile{{Field: "file", Path: p, Name: "part.zip", Offset: 3, Length: 4}})
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	raw, _ := io.ReadAll(body)
	if int64(len(raw)) != length {
		t.Fatalf("length %d != %d", len(raw), length)
	}
	_, params, _ := mime.ParseMediaType(ctype)
	mr := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
	var names []string
	var file []byte
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		names = append(names, part.FormName())
		b, _ := io.ReadAll(part)
		if part.FileName() == "part.zip" {
			file = b
		}
	}
	if len(names) != 3 || names[0] != "a" || names[1] != "b" || names[2] != "file" {
		t.Fatalf("parts = %v", names)
	}
	if string(file) != "3456" {
		t.Fatalf("file section = %q", file)
	}
}
