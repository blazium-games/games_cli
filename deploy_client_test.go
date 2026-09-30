package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// deployCmd gives a test its own credential flags and viper state, wired like
// rootCmd and initConfig. Any BLAZIUM_* variables from the developer's shell
// are cleared first.
func deployCmd(t *testing.T, env map[string]string, args ...string) *cobra.Command {
	t.Helper()
	for _, k := range []string{"BLAZIUM_ACCESS_TOKEN", "BLAZIUM_SECRET_KEY", "BLAZIUM_API_URL", "BLAZIUM_UPLOAD_URL"} {
		t.Setenv(k, env[k])
	}
	viper.Reset()
	t.Cleanup(func() {
		viper.Reset()
		pf := rootCmd.PersistentFlags()
		for _, name := range []string{"access", "secret", "url", "upload"} {
			viper.BindPFlag(name, pf.Lookup(name))
		}
	})
	cmd := &cobra.Command{Use: "test"}
	f := cmd.Flags()
	f.String("access", "", "")
	f.String("secret", "", "")
	f.Bool("secret-stdin", false, "")
	f.String("url", defaultAPIURL, "")
	f.String("upload", defaultUploadURL, "")
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"access", "secret", "url", "upload"} {
		viper.BindPFlag(name, f.Lookup(name))
	}
	initConfig()
	return cmd
}

func captureStderr(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := stderr
	stderr = &buf
	t.Cleanup(func() { stderr = old })
	return &buf
}

func fakeStdin(t *testing.T, content string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = old
		f.Close()
	})
}

func TestResolveSecretFromEnv(t *testing.T) {
	warn := captureStderr(t)
	cmd := deployCmd(t, map[string]string{"BLAZIUM_SECRET_KEY": "  " + testSecret + "\r\n"})
	got, err := resolveSecret(cmd)
	if err != nil || got != testSecret {
		t.Fatalf("secret mismatch (len %d), err = %v", len(got), err)
	}
	if warn.Len() != 0 {
		t.Fatalf("env secret must not warn: %q", warn.String())
	}
}

func TestResolveSecretFlagWinsOverEnvAndWarns(t *testing.T) {
	warn := captureStderr(t)
	cmd := deployCmd(t, map[string]string{"BLAZIUM_SECRET_KEY": "from-env"}, "--secret", "from-flag")
	got, err := resolveSecret(cmd)
	if err != nil || got != "from-flag" {
		t.Fatalf("got %q, err = %v", got, err)
	}
	if !strings.Contains(warn.String(), "--secret is visible") || strings.Contains(warn.String(), "from-flag") {
		t.Fatalf("warning = %q", warn.String())
	}
}

func TestResolveSecretStdinWinsOverFlagAndEnv(t *testing.T) {
	captureStderr(t)
	fakeStdin(t, testSecret+"\r\nsecond line\n")
	cmd := deployCmd(t, map[string]string{"BLAZIUM_SECRET_KEY": "from-env"}, "--secret-stdin", "--secret", "from-flag")
	got, err := resolveSecret(cmd)
	if err != nil || got != testSecret {
		t.Fatalf("secret mismatch (len %d), err = %v", len(got), err)
	}
}

func TestReadSecretStopsAt4096Bytes(t *testing.T) {
	got, err := readSecret(strings.NewReader(strings.Repeat("a", 5000) + "\n"))
	if err != nil || len(got) != 4096 {
		t.Fatalf("len = %d, err = %v", len(got), err)
	}
}

func TestResolveSecretMissing(t *testing.T) {
	got, err := resolveSecret(deployCmd(t, nil))
	if err != nil || got != "" {
		t.Fatalf("got %q, err = %v", got, err)
	}
}

func TestDeployClientMissingCredentials(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		args []string
		want string
	}{
		{"nothing", nil, nil, "access token is missing: set BLAZIUM_ACCESS_TOKEN (or --access)"},
		{"no secret", map[string]string{"BLAZIUM_ACCESS_TOKEN": "tok"}, nil, "the deploy key secret is missing: set BLAZIUM_SECRET_KEY or pipe it with --secret-stdin"},
		{"blank secret", map[string]string{"BLAZIUM_ACCESS_TOKEN": "tok", "BLAZIUM_SECRET_KEY": "   "}, nil, "secret is missing"},
		{"no token", map[string]string{"BLAZIUM_SECRET_KEY": testSecret}, nil, "access token is missing"},
		{"blank token flag", map[string]string{"BLAZIUM_SECRET_KEY": testSecret}, []string{"--access", " "}, "access token is missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			captureStderr(t)
			c, err := deployClient(deployCmd(t, tc.env, tc.args...))
			if c != nil || err == nil || !strings.Contains(err.Error(), tc.want) || exitCode(err) != exitUsage {
				t.Fatalf("client = %v, err = %v", c != nil, err)
			}
			if strings.Contains(err.Error(), testSecret) {
				t.Fatal("secret in error")
			}
		})
	}
}

func TestDeployClientEmptyStdinSecret(t *testing.T) {
	fakeStdin(t, "\n")
	_, err := deployClient(deployCmd(t, map[string]string{"BLAZIUM_ACCESS_TOKEN": "tok", "BLAZIUM_SECRET_KEY": "from-env"}, "--secret-stdin"))
	if err == nil || !strings.Contains(err.Error(), "secret is missing") {
		t.Fatalf("an empty stdin line must not fall back to the env secret: %v", err)
	}
}

func TestDeployClientDefaultURLs(t *testing.T) {
	c, err := deployClient(deployCmd(t, map[string]string{"BLAZIUM_ACCESS_TOKEN": " tok ", "BLAZIUM_SECRET_KEY": testSecret}))
	if err != nil {
		t.Fatal(err)
	}
	if c.baseURL != defaultAPIURL || c.uploadURL != defaultUploadURL {
		t.Fatalf("base = %s, upload = %s", c.baseURL, c.uploadURL)
	}
	if c.accessToken != "tok" || c.secretKey != testSecret {
		t.Fatal("credentials were not trimmed and stored")
	}
	if c.buildURL("/tool/info") != defaultAPIURL+"/tool/info" || c.filesURL("/tool/upload/files") != defaultUploadURL+"/tool/upload/files" {
		t.Fatalf("build = %s, files = %s", c.buildURL("/tool/info"), c.filesURL("/tool/upload/files"))
	}
}

func TestDeployClientURLPrecedence(t *testing.T) {
	creds := map[string]string{"BLAZIUM_ACCESS_TOKEN": "tok", "BLAZIUM_SECRET_KEY": testSecret}
	env := map[string]string{
		"BLAZIUM_API_URL":    "https://api.env.example/api/v1/",
		"BLAZIUM_UPLOAD_URL": " https://upload.env.example/api/v1// ",
	}
	for k, v := range creds {
		env[k] = v
	}
	c, err := deployClient(deployCmd(t, env))
	if err != nil {
		t.Fatal(err)
	}
	if c.baseURL != "https://api.env.example/api/v1" || c.uploadURL != "https://upload.env.example/api/v1" {
		t.Fatalf("env: base = %s, upload = %s", c.baseURL, c.uploadURL)
	}

	c, err = deployClient(deployCmd(t, env, "--url", "http://localhost:8080/api/v1", "--upload", "http://127.0.0.1:9000/api/v1"))
	if err != nil {
		t.Fatal(err)
	}
	if c.baseURL != "http://localhost:8080/api/v1" || c.uploadURL != "http://127.0.0.1:9000/api/v1" {
		t.Fatalf("flags: base = %s, upload = %s", c.baseURL, c.uploadURL)
	}
}

func TestDeployClientRejectsUnsafeURLs(t *testing.T) {
	cases := []struct {
		env, value, want string
	}{
		{"BLAZIUM_API_URL", "http://api.blazium.online/api/v1", "must use https"},
		{"BLAZIUM_UPLOAD_URL", "http://uploader.blazium.online/api/v1", "must use https"},
		{"BLAZIUM_API_URL", "https://user:" + testSecret + "@api.blazium.online", "must not contain credentials"},
		{"BLAZIUM_UPLOAD_URL", "uploader.blazium.online", "is not a valid URL"},
	}
	for _, tc := range cases {
		t.Run(tc.env+" "+tc.want, func(t *testing.T) {
			env := map[string]string{"BLAZIUM_ACCESS_TOKEN": "tok", "BLAZIUM_SECRET_KEY": testSecret, tc.env: tc.value}
			_, err := deployClient(deployCmd(t, env))
			if err == nil || !strings.Contains(err.Error(), tc.want) || exitCode(err) != exitUsage {
				t.Fatalf("err = %v", err)
			}
			if strings.Contains(err.Error(), testSecret) {
				t.Fatal("secret in error")
			}
		})
	}
}

func TestDeployClientSendsHeadersToBothServices(t *testing.T) {
	type seen struct{ path, token, secret, ua, accept string }
	record := func(into *seen) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			*into = seen{r.URL.Path, r.Header.Get("X-Access-Token"), r.Header.Get("X-Secret-Key"), r.Header.Get("User-Agent"), r.Header.Get("Accept")}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{}})
		}
	}
	var api, up seen
	apiSrv := httptest.NewServer(record(&api))
	defer apiSrv.Close()
	upSrv := httptest.NewServer(record(&up))
	defer upSrv.Close()

	env := map[string]string{
		"BLAZIUM_ACCESS_TOKEN": "tok",
		"BLAZIUM_SECRET_KEY":   testSecret,
		"BLAZIUM_API_URL":      apiSrv.URL + "/api/v1",
		"BLAZIUM_UPLOAD_URL":   upSrv.URL + "/api/v1",
	}
	c, err := deployClient(deployCmd(t, env))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetJSON("/tool/info"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.PostForm(c.filesURL("/tool/upload/sessions"), url.Values{"a": {"b"}}); err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]struct {
		s    seen
		path string
	}{"api": {api, "/api/v1/tool/info"}, "upload": {up, "/api/v1/tool/upload/sessions"}} {
		s := got.s
		if s.path != got.path || s.token != "tok" || s.secret != testSecret || s.ua != "chauffeur/"+version || s.accept != "application/json" {
			t.Fatalf("%s: path=%s token=%q ua=%q accept=%q secret ok=%v", name, s.path, s.token, s.ua, s.accept, s.secret == testSecret)
		}
	}
}

func TestDeployClientTransportUsesProxyButNotForLoopback(t *testing.T) {
	c, err := deployClient(deployCmd(t, map[string]string{"BLAZIUM_ACCESS_TOKEN": "tok", "BLAZIUM_SECRET_KEY": testSecret}))
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := c.httpClient.Transport.(*http.Transport)
	if !ok || tr.Proxy == nil {
		t.Fatal("transport must honor HTTP(S)_PROXY")
	}
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:9/x", nil)
	if u, err := tr.Proxy(req); err != nil || u != nil {
		t.Fatalf("loopback requests must bypass the proxy: %v, %v", u, err)
	}
}
