package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Client wraps HTTP client with authentication
type Client struct {
	baseURL     string
	uploadURL   string
	accessToken string
	secretKey   string
	httpClient  *http.Client
}

// NewClient creates a new authenticated HTTP client
func NewClient(baseURL, accessToken, secretKey string) *Client {
	return &Client{
		baseURL:     strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		accessToken: accessToken,
		secretKey:   secretKey,
		httpClient:  newHTTPClient(),
	}
}

// credentialHeaders are dropped when a redirect leaves the original host.
var credentialHeaders = []string{"X-Access-Token", "X-Secret-Key", "Authorization"}

// newHTTPClient bounds every phase of a request. The overall timeout is long
// because single-shot build uploads can be large.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 2 * time.Hour,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   30 * time.Second,
			ResponseHeaderTimeout: 10 * time.Minute,
			ExpectContinueTimeout: 1 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		},
		CheckRedirect: checkRedirect,
	}
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	first := via[0].URL
	if first.Scheme == "https" && req.URL.Scheme != "https" {
		return fmt.Errorf("refusing redirect from https to %s", req.URL.Scheme)
	}
	if !strings.EqualFold(req.URL.Host, first.Host) {
		for _, h := range credentialHeaders {
			req.Header.Del(h)
		}
	}
	return nil
}

func (c *Client) SetUploadURL(uploadURL string) {
	c.uploadURL = strings.TrimRight(strings.TrimSpace(uploadURL), "/")
}

// filesURL is an endpoint on the upload service (games_upload).
func (c *Client) filesURL(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	base := c.uploadURL
	if base == "" {
		base = c.baseURL
	}
	return base + path
}

// buildURL is an endpoint on the API (games_service). Full URLs are used as-is.
func (c *Client) buildURL(endpoint string) string {
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return endpoint
	}
	return c.baseURL + endpoint
}

// APIResponse represents the standard API response structure
type APIResponse struct {
	Success bool           `json:"success"`
	Data    map[string]any `json:"data,omitempty"`
	Error   *struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data,omitempty"`
	} `json:"error,omitempty"`
}

const maxResponseBytes = 8 << 20

func (c *Client) newRequest(method, target string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		return nil, usageErrorf("invalid URL %s: %v", redactURL(target), err)
	}
	req.Header.Set("X-Access-Token", c.accessToken)
	req.Header.Set("X-Secret-Key", c.secretKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "chauffeur/"+version)
	return req, nil
}

// do sends req and parses the {success, data, error} envelope. Failures come
// back as *apiError (the service answered) or *networkError (it didn't).
func (c *Client) do(req *http.Request) (*APIResponse, http.Header, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, &networkError{err: scrubError(err, c.secretKey, c.accessToken)}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, resp.Header, &networkError{err: fmt.Errorf("reading the response: %w", err)}
	}
	if len(raw) > maxResponseBytes {
		return nil, resp.Header, &apiError{Status: resp.StatusCode, Message: "response is larger than 8 MB"}
	}
	var apiResp APIResponse
	if err := json.Unmarshal(raw, &apiResp); err != nil {
		msg := http.StatusText(resp.StatusCode)
		if resp.StatusCode < 400 {
			msg = "the server answered with something other than JSON"
		}
		return nil, resp.Header, &apiError{Status: resp.StatusCode, Message: msg}
	}
	if !apiResp.Success || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		ae := &apiError{Status: resp.StatusCode}
		if apiResp.Error != nil {
			ae.Code, ae.Message, ae.Data = apiResp.Error.Code, apiResp.Error.Message, apiResp.Error.Data
		}
		return nil, resp.Header, ae
	}
	return &apiResp, resp.Header, nil
}

func (c *Client) sendJSON(method, target string, body any) (*APIResponse, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to encode the request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := c.newRequest(method, target, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, _, err := c.do(req)
	return resp, err
}

// GetJSON sends a GET to the API.
func (c *Client) GetJSON(endpoint string) (*APIResponse, error) {
	return c.sendJSON(http.MethodGet, c.buildURL(endpoint), nil)
}

// PostJSON sends a POST request with JSON body to the API.
func (c *Client) PostJSON(endpoint string, body any) (*APIResponse, error) {
	return c.sendJSON(http.MethodPost, c.buildURL(endpoint), body)
}

// PutJSON sends a PUT request with JSON body to the API.
func (c *Client) PutJSON(endpoint string, body any) (*APIResponse, error) {
	return c.sendJSON(http.MethodPut, c.buildURL(endpoint), body)
}

// Delete sends a DELETE to the API.
func (c *Client) Delete(endpoint string) (*APIResponse, error) {
	return c.sendJSON(http.MethodDelete, c.buildURL(endpoint), nil)
}

// PostForm sends an application/x-www-form-urlencoded POST to target.
func (c *Client) PostForm(target string, fields url.Values) (*APIResponse, http.Header, error) {
	req, err := c.newRequest(http.MethodPost, target, strings.NewReader(fields.Encode()))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req)
}

// formField is one ordered multipart text field. The upload service reads
// fields before the file, so order matters.
type formField struct{ Name, Value string }

// formFile is one file part: Length bytes of Path starting at Offset, or the
// whole file when Length < 0.
type formFile struct {
	Field  string
	Path   string
	Name   string
	Offset int64
	Length int64
}

// multipartBody streams fields and file sections without buffering file
// contents, and knows its exact length so no chunked encoding is needed.
func multipartBody(fields []formField, files []formFile) (io.Reader, int64, string, func(), error) {
	var (
		segs    []io.Reader
		total   int64
		handles []*os.File
		buf     bytes.Buffer
	)
	closeAll := func() {
		for _, f := range handles {
			f.Close()
		}
	}
	flush := func() {
		if buf.Len() == 0 {
			return
		}
		b := append([]byte(nil), buf.Bytes()...)
		segs = append(segs, bytes.NewReader(b))
		total += int64(len(b))
		buf.Reset()
	}
	mw := multipart.NewWriter(&buf)
	for _, f := range fields {
		if err := mw.WriteField(f.Name, f.Value); err != nil {
			return nil, 0, "", closeAll, err
		}
	}
	for _, ff := range files {
		fh, err := os.Open(ff.Path)
		if err != nil {
			closeAll()
			return nil, 0, "", func() {}, usageErrorf("cannot open %s: %v", ff.Path, err)
		}
		handles = append(handles, fh)
		length := ff.Length
		if length < 0 {
			st, err := fh.Stat()
			if err != nil {
				closeAll()
				return nil, 0, "", func() {}, err
			}
			length = st.Size() - ff.Offset
		}
		name := ff.Name
		if name == "" {
			name = filepath.Base(ff.Path)
		}
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, ff.Field, name))
		h.Set("Content-Type", "application/octet-stream")
		if _, err := mw.CreatePart(h); err != nil {
			closeAll()
			return nil, 0, "", func() {}, err
		}
		flush()
		segs = append(segs, io.NewSectionReader(fh, ff.Offset, length))
		total += length
	}
	if err := mw.Close(); err != nil {
		closeAll()
		return nil, 0, "", func() {}, err
	}
	flush()
	return io.MultiReader(segs...), total, mw.FormDataContentType(), closeAll, nil
}

// PostMultipart streams fields and files to target.
func (c *Client) PostMultipart(target string, fields []formField, files []formFile, headers map[string]string) (*APIResponse, http.Header, error) {
	body, length, ctype, done, err := multipartBody(fields, files)
	if err != nil {
		return nil, nil, err
	}
	defer done()
	req, err := c.newRequest(http.MethodPost, target, body)
	if err != nil {
		return nil, nil, err
	}
	req.ContentLength = length
	req.Header.Set("Content-Type", ctype)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return c.do(req)
}

// scrubError removes credentials from an error message, in case a proxy or
// transport ever echoes them.
func scrubError(err error, secrets ...string) error {
	msg := err.Error()
	changed := false
	for _, s := range secrets {
		if len(s) >= 4 && strings.Contains(msg, s) {
			msg = strings.ReplaceAll(msg, s, "[redacted]")
			changed = true
		}
	}
	if !changed {
		return err
	}
	return fmt.Errorf("%s", msg)
}

// checkServiceURL refuses to send the deploy key anywhere but https, except to
// a loopback host for local testing.
func checkServiceURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return usageErrorf("service URL %s is not a valid URL", redactURL(raw))
	}
	if u.User != nil {
		return usageErrorf("service URL %s must not contain credentials", redactURL(raw))
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
	}
	return usageErrorf("service URL %s must use https (http is only allowed for localhost)", redactURL(raw))
}

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(invalid URL)"
	}
	u.User = nil
	u.RawQuery = ""
	return u.String()
}
