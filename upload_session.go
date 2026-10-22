package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// scanPollInterval is the wait between upload scan polls. Tests shrink it.
var scanPollInterval = 2 * time.Second

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
	Path       string
	Size       int64
	Checksum   string
	BuildID    string
	Platform   PlatformSpec
	App        string
	WebEngine  string
	WebVersion string
	WebVariant string
}

func (u buildFileUpload) fields() []formField {
	out := []formField{
		{"build_id", u.BuildID},
		{"os", u.Platform.OS},
		{"arch", u.Platform.Arch},
		{"channel", u.Platform.Channel},
		{"checksum", u.Checksum},
	}
	if u.App != "" {
		out = append(out, formField{"app", u.App})
	}
	if u.Platform.OS == "web" {
		out = append(out,
			formField{"web_engine", u.WebEngine},
			formField{"web_version", u.WebVersion},
			formField{"web_variant", u.WebVariant},
		)
	}
	return out
}

// uploadBuildFile sends one zip: a single streamed request up to 64 MB,
// otherwise a chunked session. It returns the completed upload's data.
func uploadBuildFile(c *Client, u buildFileUpload) (map[string]any, error) {
	if u.Size <= 0 || u.Size > maxBuildFileBytes {
		return nil, usageErrorf("%s is %s; build files must be between 1 byte and 5 GB", u.Path, sizeText(u.Size))
	}
	var data map[string]any
	var err error
	if u.Size <= singleShotMaxBytes {
		headers := map[string]string{"Idempotency-Key": idempotencyKey(u.Checksum, u.BuildID)}
		data, err = withRetry("upload", func() (map[string]any, error) {
			resp, _, err := c.PostMultipart(c.filesURL("/tool/upload/files"), u.fields(),
				[]formFile{{Field: "file", Path: u.Path, Length: -1}}, headers)
			if err != nil {
				return nil, err
			}
			return resp.Data, nil
		})
	} else {
		data, err = uploadInChunks(c, u)
	}
	if err != nil {
		return nil, err
	}
	return waitForClean(c, data)
}

// waitForClean polls until the scan finishes. Only clean is success.
// pending and scanning stay in progress. infected, error, and any other
// status fail immediately, including on the first response. A 404 while
// polling means the file was removed after processing failed (4099).
func waitForClean(c *Client, data map[string]any) (map[string]any, error) {
	if done, err := classifyUpload(data); err != nil || done != nil {
		return done, err
	}
	uid, _ := data["file_uid"].(string)
	if uid == "" {
		return nil, &apiError{Status: 200, Message: "the upload service did not return a file_uid"}
	}
	deadline := time.Now().Add(35 * time.Minute)
	for {
		if time.Now().After(deadline) {
			return nil, &apiError{Status: 504, Message: "the upload was still being scanned when chauffeur stopped waiting"}
		}
		time.Sleep(scanPollInterval)
		resp, err := c.GetJSON(c.filesURL("/tool/upload/files/" + url.PathEscape(uid)))
		if err != nil {
			var ae *apiError
			if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
				return nil, &apiError{Status: http.StatusConflict, Code: 4099, Message: "the upload was removed after processing failed"}
			}
			if shouldRetry(err) {
				continue
			}
			return nil, err
		}
		data = resp.Data
		if done, err := classifyUpload(data); err != nil || done != nil {
			return done, err
		}
	}
}

// classifyUpload accepts clean, keeps pending and scanning in progress, and
// rejects every other status. A nil result with a nil error means keep polling.
func classifyUpload(data map[string]any) (map[string]any, error) {
	status, _ := data["status"].(string)
	switch status {
	case "clean":
		if note, _ := data["error"].(string); note != "" {
			logf("Warning: %s\n", note)
		}
		return data, nil
	case "pending", "scanning":
		return nil, nil
	case "error", "infected":
		msg, _ := data["error"].(string)
		if msg == "" {
			msg = "the upload did not pass processing"
		}
		return nil, &apiError{Status: http.StatusConflict, Message: msg}
	default:
		if status == "" {
			status = "missing"
		}
		return nil, &apiError{Status: http.StatusConflict, Message: "unexpected upload status " + status}
	}
}

func idempotencyKey(checksum, buildID string) string {
	return checksum + ":" + buildID
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
	open := func() (string, int64, error) {
		opened, err := withRetry("opening the upload session", func() (map[string]any, error) {
			resp, _, err := c.PostForm(c.filesURL("/tool/upload/sessions"), form)
			if err != nil {
				return nil, err
			}
			return resp.Data, nil
		})
		if err != nil {
			return "", 0, err
		}
		id, _ := opened["session_id"].(string)
		if id == "" {
			return "", 0, &apiError{Status: 200, Message: "the upload service did not return a session_id"}
		}
		off := int64(0)
		if cur, exists := opened["current_size"]; exists {
			n, ok := offsetOf(cur)
			if !ok || n > u.Size {
				return "", 0, &apiError{Status: http.StatusBadGateway, Message: fmt.Sprintf("the upload service reported an offset outside the %d byte file", u.Size)}
			}
			off = n
		}
		logf("  upload session %s (%s in %s chunks)\n", id, sizeText(u.Size), sizeText(chunkBytes))
		return id, off, nil
	}
	sessionID, offset, err := open()
	if err != nil {
		return nil, err
	}

	tries, reopened, stalled := 0, 0, 0
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
			isAPI := errors.As(err, &ae)
			// 4045: the session expired or the server lost it. Start over in a new one.
			if isAPI && ae.Code == 4045 && reopened < maxSessionReopens {
				reopened++
				logf("  the upload session is gone; opening a new one (%d/%d)\n", reopened, maxSessionReopens)
				if sessionID, offset, err = open(); err != nil {
					return nil, err
				}
				tries, stalled = 0, 0
				continue
			}
			// 4044 with current_size: the server has a different amount of the file
			// than we thought (a lost acknowledgement or a failed write). Resume there.
			if isAPI && ae.Code == 4044 && (ae.Status == 400 || ae.Status == 409) && ae.Data != nil && tries < maxChunkTries {
				if cur, ok := ae.Data["current_size"]; ok {
					next, oerr := applyServerOffset(offset, u.Size, cur, &stalled)
					if oerr != nil {
						return nil, oerr
					}
					offset = next
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
			next, oerr := applyServerOffset(offset, u.Size, cur, &stalled)
			if oerr != nil {
				return nil, oerr
			}
			offset = next
		} else {
			offset += length
			stalled = 0
		}
		logf("  uploaded %s of %s (%.0f%%)\n", sizeText(offset), sizeText(u.Size), float64(offset)*100/float64(u.Size))
	}
	return nil, &apiError{Status: 200, Message: "the upload finished without a file_uid"}
}

// offsetOf parses a server byte offset. Negative, fractional, and oversized
// values are rejected so a bad current_size cannot walk the file pointer.
func offsetOf(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		if n < 0 || n > float64(maxBuildFileBytes) || n != float64(int64(n)) {
			return 0, false
		}
		return int64(n), true
	case int64:
		if n < 0 || n > maxBuildFileBytes {
			return 0, false
		}
		return n, true
	case int:
		if n < 0 || int64(n) > maxBuildFileBytes {
			return 0, false
		}
		return int64(n), true
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		if err != nil || i < 0 || i > maxBuildFileBytes {
			return 0, false
		}
		return i, true
	default:
		return 0, false
	}
}

// applyServerOffset accepts a current_size inside the file. An offset that
// does not move forward is counted, and the upload fails after maxChunkTries
// responses that make no progress.
func applyServerOffset(current, size int64, raw any, stalled *int) (int64, error) {
	next, ok := offsetOf(raw)
	if !ok || next > size {
		return 0, &apiError{Status: http.StatusBadGateway, Message: fmt.Sprintf("the upload service reported an offset outside the %d byte file", size)}
	}
	if next <= current {
		*stalled++
		if *stalled >= maxChunkTries {
			return 0, &apiError{Status: http.StatusBadGateway, Message: "the upload made no progress"}
		}
	} else {
		*stalled = 0
	}
	return next, nil
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
