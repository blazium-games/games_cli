package main

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// placeholderAccountToken is an obvious stand-in for BLAZIUM_USER_TOKEN.
// It is not a credential.
const placeholderAccountToken = "placeholder-user-token"

// accountTokenCommands are every patron operation that calls printUserAPI.
var accountTokenCommands = []struct {
	name string
	args []string
}{
	{"status", []string{"patron", "status"}},
	{"status-developer", []string{"patron", "status", "--developer", "some-developer"}},
	{"list", []string{"patron", "list"}},
	{"get", []string{"patron", "get", "some-user"}},
	{"tiers", []string{"patron", "tiers"}},
	{"tier-create", []string{"patron", "tier", "create", "--name", "tier-name", "--price-cents", "50"}},
	{"tier-update", []string{"patron", "tier", "update", "tier-uid", "--name", "tier-name"}},
	{"tier-delete", []string{"patron", "tier", "delete", "tier-uid"}},
	{"posts", []string{"patron", "posts"}},
	{"post-create", []string{"patron", "post", "create", "--title", "post-title"}},
	{"post-update", []string{"patron", "post", "update", "post-uid", "--title", "post-title"}},
	{"post-delete", []string{"patron", "post", "delete", "post-uid"}},
}

type rawReq struct {
	raw []byte
}

type rawCap struct {
	ln   net.Listener
	addr string
	host string
	mu   sync.Mutex
	reqs []rawReq
}

func listenRaw(t *testing.T, host string) *rawCap {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatal(err)
	}
	c := &rawCap{ln: ln, addr: ln.Addr().String(), host: host}
	go c.serve()
	t.Cleanup(func() { ln.Close() })
	return c
}

func (c *rawCap) baseURL() string {
	return "http://" + c.addr
}

func (c *rawCap) serve() {
	for {
		conn, err := c.ln.Accept()
		if err != nil {
			return
		}
		go c.handle(conn)
	}
}

func (c *rawCap) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for len(buf) < 64<<10 {
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if bytes.Contains(buf, []byte("\r\n\r\n")) || err != nil || nonHTTP(buf) {
			break
		}
	}
	c.mu.Lock()
	c.reqs = append(c.reqs, rawReq{raw: append([]byte(nil), buf...)})
	c.mu.Unlock()
	if bytes.Contains(buf, []byte("\r\n\r\n")) {
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 11\r\nConnection: close\r\n\r\n{\"data\":{}}"))
	}
}

func nonHTTP(buf []byte) bool {
	if len(buf) == 0 {
		return false
	}
	switch buf[0] {
	case 'G', 'P', 'D', 'H', 'O', 'C', 'T':
		return false
	default:
		return true
	}
}

func (c *rawCap) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.reqs)
}

func (c *rawCap) since(n int) []rawReq {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n > len(c.reqs) {
		n = len(c.reqs)
	}
	out := make([]rawReq, len(c.reqs)-n)
	copy(out, c.reqs[n:])
	return out
}

func carriesAccountToken(raw []byte) bool {
	return bytes.Contains(raw, []byte("Authorization: Bearer "+placeholderAccountToken))
}

func cleartextRequest(raw []byte) bool {
	if len(raw) == 0 || raw[0] == 0x16 {
		return false
	}
	for _, method := range []string{"GET ", "POST ", "PUT ", "DELETE "} {
		if bytes.HasPrefix(raw, []byte(method)) {
			return true
		}
	}
	return false
}

func nonLoopbackIPv4(t *testing.T) string {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ln, err := net.Listen("tcp", net.JoinHostPort(ip.String(), "0"))
			if err != nil {
				continue
			}
			ln.Close()
			return ip.String()
		}
	}
	t.Fatal("no non-loopback IPv4 address is available")
	return ""
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// guardPatronClient wraps the real patron client so a request aimed at any
// host other than the fixtures stops before a dial. Fixture hosts still use
// newHTTPClient's transport, redirect check, and header writer.
func guardPatronClient(t *testing.T, hosts ...string) {
	t.Helper()
	allowed := map[string]bool{"127.0.0.1": true, "localhost": true, "::1": true}
	for _, host := range hosts {
		allowed[host] = true
	}
	old := patronHTTPClient
	patronHTTPClient = func() *http.Client {
		client := newHTTPClient()
		client.Timeout = 3 * time.Second
		base := client.Transport
		client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if !allowed[r.URL.Hostname()] {
				return nil, errors.New("blocked test request host " + r.URL.Hostname())
			}
			return base.RoundTrip(r)
		})
		return client
	}
	t.Cleanup(func() { patronHTTPClient = old })
}

func resetFlagSet(fs *pflag.FlagSet) {
	if fs == nil {
		return
	}
	fs.VisitAll(func(f *pflag.Flag) {
		if !f.Changed {
			return
		}
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
}

func resetCommandFlags(cmd *cobra.Command) {
	resetFlagSet(cmd.Flags())
	resetFlagSet(cmd.PersistentFlags())
	for _, child := range cmd.Commands() {
		resetCommandFlags(child)
	}
}

func silenceStdout(t *testing.T) func() {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, r)
		close(done)
	}()
	return func() {
		_ = w.Close()
		<-done
		os.Stdout = old
		_ = r.Close()
	}
}

func runChauffeur(t *testing.T, env map[string]string, args ...string) error {
	t.Helper()
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		t.Setenv(key, "")
	}
	for key, value := range env {
		t.Setenv(key, value)
	}
	if _, ok := env["BLAZIUM_USER_TOKEN"]; !ok {
		t.Setenv("BLAZIUM_USER_TOKEN", placeholderAccountToken)
	}
	resetCommandFlags(rootCmd)
	apiURL = defaultAPIURL
	jsonOutput = false
	rootCmd.SetArgs(args)
	restoreStdout := silenceStdout(t)
	defer restoreStdout()
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		resetCommandFlags(rootCmd)
		apiURL = defaultAPIURL
		jsonOutput = false
	})
	return rootCmd.Execute()
}

func requireNoRequest(t *testing.T, reqs []rawReq) {
	t.Helper()
	for _, req := range reqs {
		if carriesAccountToken(req.raw) && cleartextRequest(req.raw) {
			t.Fatal("account token was sent in a cleartext HTTP request")
		}
	}
	if len(reqs) != 0 {
		t.Fatal("request was sent before the service URL was refused")
	}
}

func requireUsage(t *testing.T, err error, snippet string) {
	t.Helper()
	if err == nil {
		t.Fatal("command succeeded")
	}
	if strings.Contains(err.Error(), placeholderAccountToken) {
		t.Fatal("error contains the account token")
	}
	var usage *usageError
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), snippet) {
		t.Fatalf("got %v", err)
	}
}

func TestAccountTokenURLCleartextCommands(t *testing.T) {
	attacker := listenRaw(t, nonLoopbackIPv4(t))
	guardPatronClient(t, attacker.host)
	for _, tc := range accountTokenCommands {
		t.Run(tc.name, func(t *testing.T) {
			before := attacker.len()
			args := append([]string{"--url", attacker.baseURL()}, tc.args...)
			err := runChauffeur(t, nil, args...)
			requireNoRequest(t, attacker.since(before))
			requireUsage(t, err, "must use https")
		})
	}
	t.Run("list-user-token-flag", func(t *testing.T) {
		before := attacker.len()
		err := runChauffeur(t, map[string]string{"BLAZIUM_USER_TOKEN": ""},
			"--url", attacker.baseURL(), "patron", "list", "--user-token", placeholderAccountToken)
		requireNoRequest(t, attacker.since(before))
		requireUsage(t, err, "must use https")
	})
}

func TestAccountTokenURLDecoyLoopback(t *testing.T) {
	attacker := listenRaw(t, nonLoopbackIPv4(t))
	guardPatronClient(t, attacker.host)
	for _, raw := range []string{
		"http://127.0.0.1:9@" + attacker.addr,
		"http://localhost@" + attacker.addr,
	} {
		t.Run(raw, func(t *testing.T) {
			before := attacker.len()
			err := runChauffeur(t, nil, "--url", raw, "patron", "list")
			requireNoRequest(t, attacker.since(before))
			requireUsage(t, err, "must not contain credentials")
		})
	}
}

func TestAccountTokenURLFlagAndEnvAreOneValue(t *testing.T) {
	attacker := listenRaw(t, nonLoopbackIPv4(t))
	loop := listenRaw(t, "127.0.0.1")
	guardPatronClient(t, attacker.host, loop.host)

	t.Run("cleartext-flag", func(t *testing.T) {
		attackerBefore, loopBefore := attacker.len(), loop.len()
		err := runChauffeur(t, map[string]string{"BLAZIUM_API_URL": loop.baseURL()},
			"--url", attacker.baseURL(), "patron", "list")
		requireNoRequest(t, attacker.since(attackerBefore))
		requireNoRequest(t, loop.since(loopBefore))
		requireUsage(t, err, "must use https")
	})

	t.Run("loopback-flag", func(t *testing.T) {
		attackerBefore, loopBefore := attacker.len(), loop.len()
		err := runChauffeur(t, map[string]string{"BLAZIUM_API_URL": attacker.baseURL()},
			"--url", loop.baseURL(), "patron", "list")
		requireNoRequest(t, attacker.since(attackerBefore))
		got := loop.since(loopBefore)
		if len(got) != 1 || !carriesAccountToken(got[0].raw) || !cleartextRequest(got[0].raw) {
			t.Fatal("loopback --url did not receive the account token in a cleartext request")
		}
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestAccountTokenURLLoopbackReceivesToken(t *testing.T) {
	loop := listenRaw(t, "127.0.0.1")
	_, port, err := net.SplitHostPort(loop.addr)
	if err != nil {
		t.Fatal(err)
	}
	guardPatronClient(t, loop.host)
	for _, raw := range []string{loop.baseURL(), "http://localhost:" + port} {
		t.Run(raw, func(t *testing.T) {
			before := loop.len()
			err := runChauffeur(t, nil, "--url", raw, "patron", "list")
			got := loop.since(before)
			if len(got) != 1 || !carriesAccountToken(got[0].raw) || !cleartextRequest(got[0].raw) {
				t.Fatal("loopback URL did not receive the account token in a cleartext request")
			}
			if !bytes.Contains(got[0].raw, []byte("GET /private/patrons HTTP/1.1")) {
				t.Fatal("patron list did not request /private/patrons")
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}

	t.Run("ipv6", func(t *testing.T) {
		v6 := listenRaw(t, "::1")
		guardPatronClient(t, "::1")
		before := v6.len()
		err := runChauffeur(t, nil, "--url", v6.baseURL(), "patron", "list")
		got := v6.since(before)
		if len(got) != 1 || !carriesAccountToken(got[0].raw) {
			t.Fatal("IPv6 loopback URL did not receive the account token")
		}
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestAccountTokenURLRedirectDoesNotForwardToken(t *testing.T) {
	attacker := listenRaw(t, nonLoopbackIPv4(t))
	var mu sync.Mutex
	var auth string
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = r.Header.Get("Authorization")
		mu.Unlock()
		http.Redirect(w, r, attacker.baseURL()+"/steal", http.StatusFound)
	}))
	t.Cleanup(loop.Close)
	guardPatronClient(t, "127.0.0.1", attacker.host)

	before := attacker.len()
	err := runChauffeur(t, nil, "--url", loop.URL, "patron", "list")
	mu.Lock()
	gotAuth := auth
	mu.Unlock()
	if gotAuth != "Bearer "+placeholderAccountToken {
		t.Fatal("loopback request did not carry the account token")
	}
	extra := attacker.since(before)
	for _, req := range extra {
		if carriesAccountToken(req.raw) {
			t.Fatal("redirect target received the account token")
		}
	}
	if len(extra) != 0 {
		t.Fatal("redirect target was contacted after the URL check")
	}
	if err == nil {
		t.Fatal("redirect completed")
	}
	if strings.Contains(err.Error(), placeholderAccountToken) {
		t.Fatal("error contains the account token")
	}
}

func TestAccountTokenURLNetworkErrorOmitsToken(t *testing.T) {
	var mu sync.Mutex
	var auth string
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = r.Header.Get("Authorization")
		mu.Unlock()
		http.Redirect(w, r, "http://"+placeholderAccountToken+".invalid/leak", http.StatusFound)
	}))
	t.Cleanup(loop.Close)
	guardPatronClient(t, "127.0.0.1")

	err := runChauffeur(t, nil, "--url", loop.URL, "patron", "list")
	mu.Lock()
	gotAuth := auth
	mu.Unlock()
	if gotAuth != "Bearer "+placeholderAccountToken {
		t.Fatal("loopback request did not carry the account token")
	}
	if err == nil {
		t.Fatal("expected a network error")
	}
	if strings.Contains(err.Error(), placeholderAccountToken) {
		t.Fatal("network error contains the account token")
	}
}

func TestAccountTokenURLRejectsUnsafeBases(t *testing.T) {
	loop := listenRaw(t, "127.0.0.1")
	guardPatronClient(t, loop.host)
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"userinfo", "http://name:secret@" + loop.addr, "must not contain credentials"},
		{"https-userinfo", "https://name:secret@" + loop.addr, "must not contain credentials"},
		{"file", "file:///etc/passwd", "is not a valid URL"},
		{"missing-host", "http:///private", "is not a valid URL"},
		{"not-url", "not a url", "is not a valid URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := loop.len()
			err := runChauffeur(t, nil, "--url", tc.raw, "patron", "list")
			requireNoRequest(t, loop.since(before))
			requireUsage(t, err, tc.want)
		})
	}
}
