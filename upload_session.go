package main

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"time"
)

// backoff is the wait before retry attempt n (1-based). Tests replace it.
var backoff = func(n int) time.Duration {
	d := time.Second << (n - 1)
	if d > 30*time.Second {
		d = 30 * time.Second
	}
	return d
}

// buildFileUpload is one build zip bound for games_upload.
type buildFileUpload struct {
	Path     string
	Size     int64
	Checksum string
	BuildID  string
	Platform PlatformSpec
}

func (u buildFileUpload) fields() []formField {
	return []formField{
		{"build_id", u.BuildID},
		{"os", u.Platform.OS},
		{"arch", u.Platform.Arch},
		{"channel", u.Platform.Channel},
		{"checksum", u.Checksum},
	}
}

// uploadBuildFile sends one zip: a single streamed request up to 64 MB,
// otherwise a chunked session. It returns the completed upload's data.
func uploadBuildFile(c *Client, u buildFileUpload) (map[string]any, error) {
	if u.Size <= 0 || u.Size > maxBuildFileBytes {
		return nil, usageErrorf("%s is %s; build files must be between 1 byte and 5 GB", u.Path, sizeText(u.Size))
	}
	if u.Size <= singleShotMaxBytes {
		return withRetry("upload", func() (map[string]any, error) {
			resp, _, err := c.PostMultipart(c.filesURL("/tool/upload/files"), u.fields(),
				[]formFile{{Field: "file", Path: u.Path, Length: -1}}, nil)
			if err != nil {
				return nil, err
			}
			return resp.Data, nil
		})
	}
	return uploadInChunks(c, u)
}

// withRetry retries network errors, rate limits and server errors with backoff.
func withRetry(what string, call func() (map[string]any, error)) (map[string]any, error) {
	var lastErr error
	for attempt := 1; attempt <= maxChunkTries; attempt++ {
		data, err := call()
		if err == nil {
			return data, nil
		}
		lastErr = err
		if !shouldRetry(err) || attempt == maxChunkTries {
			break
		}
		wait := backoff(attempt)
		logf("  %s failed (%v); retrying in %s (%d/%d)\n", what, firstLine(err), wait, attempt, maxChunkTries-1)
		time.Sleep(wait)
	}
	return nil, lastErr
}

func shouldRetry(err error) bool {
	var ne *networkError
	var ae *apiError
	if errors.As(err, &ne) {
		return true
	}
	return errors.As(err, &ae) && ae.retryable()
}

func uploadInChunks(c *Client, u buildFileUpload) (map[string]any, error) {
	form := url.Values{}
	for _, f := range u.fields() {
		form.Set(f.Name, f.Value)
	}
	form.Set("filename", filepath.Base(u.Path))
	form.Set("total_size", strconv.FormatInt(u.Size, 10))
	opened, err := withRetry("opening the upload session", func() (map[string]any, error) {
		resp, _, err := c.PostForm(c.filesURL("/tool/upload/sessions"), form)
		if err != nil {
			return nil, err
		}
		return resp.Data, nil
	})
	if err != nil {
		return nil, err
	}
	sessionID, _ := opened["session_id"].(string)
	if sessionID == "" {
		return nil, &apiError{Status: 200, Message: "the upload service did not return a session_id"}
	}
	logf("  upload session %s (%s in %s chunks)\n", sessionID, sizeText(u.Size), sizeText(chunkBytes))

	offset := int64Of(opened["current_size"])
	tries := 0
	for offset < u.Size {
		length := min(chunkBytes, u.Size-offset)
		resp, _, err := c.PostMultipart(c.filesURL("/tool/upload/files"), nil,
			[]formFile{{Field: "file", Path: u.Path, Name: filepath.Base(u.Path), Offset: offset, Length: length}},
			map[string]string{
				"X-Upload-Session-ID": sessionID,
				"Content-Range":       fmt.Sprintf("bytes %d-%d/%d", offset, offset+length-1, u.Size),
			})
		if err != nil {
			var ae *apiError
			tries++
			if errors.As(err, &ae) && ae.Status == 409 && ae.Data != nil && tries < maxChunkTries {
				if cur, ok := ae.Data["current_size"]; ok {
					offset = int64Of(cur)
					logf("  resuming at byte %d\n", offset)
					continue
				}
			}
			if (!shouldRetry(err) && !(errors.As(err, &ae) && ae.Code == 4047)) || tries >= maxChunkTries {
				return nil, err
			}
			wait := backoff(tries)
			logf("  chunk at byte %d failed (%v); retrying in %s (%d/%d)\n", offset, firstLine(err), wait, tries, maxChunkTries-1)
			time.Sleep(wait)
			continue
		}
		tries = 0
		if _, done := resp.Data["file_uid"]; done {
			logf("  uploaded %s (100%%)\n", sizeText(u.Size))
			return resp.Data, nil
		}
		if cur, ok := resp.Data["current_size"]; ok {
			offset = int64Of(cur)
		} else {
			offset += length
		}
		logf("  uploaded %s of %s (%.0f%%)\n", sizeText(offset), sizeText(u.Size), float64(offset)*100/float64(u.Size))
	}
	return nil, &apiError{Status: 200, Message: "the upload finished without a file_uid"}
}

func int64Of(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	}
	return 0
}

func firstLine(err error) string {
	s := err.Error()
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
