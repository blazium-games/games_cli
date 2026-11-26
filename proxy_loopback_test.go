package main

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// TestLoopbackDeployKeyStaysOffProxy is the spec for the reported proxy leak.
// It fails while a loopback API call that checkServiceURL allows delivers
// X-Access-Token or X-Secret-Key to a proxy selected from the environment.
//
// Each case runs in a fresh process. net/http reads HTTP_PROXY once, on the
// first proxied request, so the variable has to be set before that call.
func TestLoopbackDeployKeyStaysOffProxy(t *testing.T) {
	if os.Getenv("CHAUFFEUR_PROXY_CHILD") != "1" {
		envs := []string{"HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy", "HTTPS_PROXY"}
		hosts := []string{
			"127.0.0.1",
			"127.0.0.2",
			"localhost",
			"LocalHost",
			"LOCALHOST",
			"lOcAlHoSt",
			"::1",
			"0:0:0:0:0:0:0:1",
			"::ffff:127.0.0.1",
		}
		for _, envName := range envs {
			for _, host := range hosts {
				envName, host := envName, host
				t.Run(envName+"/"+host, func(t *testing.T) {
					cmd := exec.Command(os.Args[0], "-test.run=^TestLoopbackDeployKeyStaysOffProxy$", "-test.count=1", "-test.v")
					cmd.Env = proxyChildEnv(envName, host)
					out, err := cmd.CombinedOutput()
					t.Log(string(out))
					if err != nil {
						t.Fatalf("child: %v", err)
					}
				})
			}
		}
		return
	}
	runLoopbackProxyChild(t)
}

func proxyChildEnv(envName, host string) []string {
	drop := map[string]bool{
		"HTTP_PROXY": true, "http_proxy": true,
		"HTTPS_PROXY": true, "https_proxy": true,
		"ALL_PROXY": true, "all_proxy": true,
		"NO_PROXY": true, "no_proxy": true,
		"REQUEST_METHOD": true,
	}
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if drop[name] || strings.HasPrefix(name, "CHAUFFEUR_PROXY_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"CHAUFFEUR_PROXY_CHILD=1",
		"CHAUFFEUR_PROXY_ENV="+envName,
		"CHAUFFEUR_PROXY_HOST="+host,
	)
	return env
}

func runLoopbackProxyChild(t *testing.T) {
	t.Helper()
	envName := os.Getenv("CHAUFFEUR_PROXY_ENV")
	host := os.Getenv("CHAUFFEUR_PROXY_HOST")

	var proxyHits []proxyHit
	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxySrv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits = append(proxyHits, proxyHit{
			uri:    r.RequestURI,
			token:  r.Header.Get("X-Access-Token") != "",
			secret: r.Header.Get("X-Secret-Key") != "",
			auth:   r.Header.Get("Authorization") != "",
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{}}`)
	})}
	go proxySrv.Serve(proxyLn)
	t.Cleanup(func() { proxySrv.Close() })

	// Set the proxy before the first ProxyFromEnvironment call in this process.
	patronHTTPClient = func() *http.Client {
		c := newHTTPClient()
		if tr, ok := c.Transport.(*http.Transport); ok {
			tr.DialContext = (&net.Dialer{Timeout: 2 * time.Second}).DialContext
		}
		return c
	}
	proxyURL := "http://" + proxyLn.Addr().String()
	if err := os.Setenv(envName, proxyURL); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy"} {
		if k != envName {
			os.Unsetenv(k)
		}
	}

	originLn, err := listenLoopback(host)
	if err != nil {
		t.Fatal(err)
	}
	var originHits int
	originSrv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHits++
		if r.Header.Get("X-Access-Token") == "" && r.Header.Get("Authorization") == "" {
			http.Error(w, "missing credential", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{}}`)
	})}
	go originSrv.Serve(originLn)
	t.Cleanup(func() { originSrv.Close() })

	_, port, err := net.SplitHostPort(originLn.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	api := "http://" + net.JoinHostPort(host, port)

	// Control: a non-loopback http URL must reach an HTTP_PROXY. If it does
	// not, this process is not actually honoring the variable and a later
	// "proxy saw nothing" result would be meaningless. ALL_PROXY and
	// HTTPS_PROXY are not the variables net/http uses for http URLs, so
	// they must not be probed with an outside host.
	wantsHTTPProxy := envName == "HTTP_PROXY" || envName == "http_proxy"
	controlProxied := false
	if wantsHTTPProxy {
		controlClient := newHTTPClient()
		if tr, ok := controlClient.Transport.(*http.Transport); ok {
			tr.DialContext = (&net.Dialer{Timeout: 2 * time.Second}).DialContext
		}
		control, err := controlClient.Get("http://example.com/probe")
		if err != nil {
			t.Fatalf("control request: %v", err)
		}
		control.Body.Close()
		controlProxied = len(proxyHits) > 0 && strings.Contains(proxyHits[0].uri, "example.com")
		proxyHits = nil
		if !controlProxied {
			t.Fatal("HTTP_PROXY was set but the non-loopback control request did not reach the proxy")
		}
	}

	if err := checkServiceURL(api); err != nil {
		t.Fatalf("host %s is not an allowed loopback API URL: %v", host, err)
	}
	if err := exerciseDeployCalls(t, api); err != nil {
		t.Logf("deploy calls: %v", err)
	}
	if err := exercisePatronCall(t, api); err != nil {
		t.Logf("patron call: %v", err)
	}

	leaked := false
	for _, hit := range proxyHits {
		if hit.token || hit.secret || hit.auth {
			leaked = true
			t.Errorf("proxy received a credential header on %s (access header set=%v secret header set=%v authorization set=%v)", hit.uri, hit.token, hit.secret, hit.auth)
		}
	}
	t.Logf("env=%s host=%s controlProxied=%v originHits=%d proxyHits=%d leaked=%v api=%s", envName, host, controlProxied, originHits, len(proxyHits), leaked, api)
	if wantsHTTPProxy && !leaked && originHits == 0 {
		t.Fatal("loopback call reached neither the origin nor the proxy")
	}
}

type proxyHit struct {
	uri           string
	token, secret bool
	auth          bool
}

func listenLoopback(host string) (net.Listener, error) {
	switch strings.ToLower(host) {
	case "::1", "0:0:0:0:0:0:0:1":
		return net.Listen("tcp", "[::1]:0")
	case "::ffff:127.0.0.1":
		return net.Listen("tcp", "127.0.0.1:0")
	case "127.0.0.2":
		return net.Listen("tcp", "127.0.0.2:0")
	default:
		return net.Listen("tcp", "127.0.0.1:0")
	}
}

func exerciseDeployCalls(t *testing.T, api string) error {
	t.Helper()
	c, err := deployClient(deployCmd(t, map[string]string{
		"BLAZIUM_ACCESS_TOKEN": "placeholder-access",
		"BLAZIUM_SECRET_KEY":   "placeholder-secret",
		"BLAZIUM_API_URL":      api,
		"BLAZIUM_UPLOAD_URL":   api,
	}))
	if err != nil {
		return err
	}
	// Keep a failed direct dial short. Proxy selection is unchanged: the
	// transport still uses the Proxy function installed by newHTTPClient.
	if tr, ok := c.httpClient.Transport.(*http.Transport); ok {
		tr.DialContext = (&net.Dialer{Timeout: 2 * time.Second}).DialContext
	}
	if _, err := c.GetJSON("/tool/info"); err != nil {
		return err
	}
	if _, err := c.PostJSON("/tool/post", map[string]string{"k": "v"}); err != nil {
		return err
	}
	if _, err := c.PutJSON("/tool/put", map[string]string{"k": "v"}); err != nil {
		return err
	}
	if _, err := c.Delete("/tool/delete"); err != nil {
		return err
	}
	if _, _, err := c.PostForm(c.filesURL("/tool/upload/sessions"), url.Values{"a": {"b"}}); err != nil {
		return err
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "a.bin")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		return err
	}
	if _, _, err := c.PostMultipart(c.filesURL("/tool/upload/build"), nil, []formFile{{
		Field: "file", Path: file, Name: "a.bin", Offset: 0, Length: -1,
	}}, nil); err != nil {
		return err
	}
	return c.DownloadFile(api+"/tool/file", filepath.Join(dir, "out.bin"))
}

func exercisePatronCall(t *testing.T, api string) error {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().String("user-token", "", "")
	if err := cmd.Flags().Set("user-token", "placeholder-user-token"); err != nil {
		return err
	}
	apiURL = api
	return printUserAPI(cmd, http.MethodGet, "/private/me", nil)
}
