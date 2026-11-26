package main

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// cdnStandIn is a local stand-in for https://cdn.blazium.online. The product
// client pins nothing and follows HTTPS_PROXY (net/http.ProxyFromEnvironment),
// so tests can serve the emulator object without contacting the real CDN.
type cdnStandIn struct {
	mu           sync.Mutex
	files        map[string][]byte
	paths        []string
	handshakeErr string
	leaf         tls.Certificate
	proxyURL     string
}

var sapiCDN cdnStandIn

func TestMain(m *testing.M) {
	if err := sapiCDN.start(); err != nil {
		fmt.Fprintf(os.Stderr, "cdn stand-in: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func (c *cdnStandIn) start() error {
	caDER, caKey, err := newTestCA()
	if err != nil {
		return err
	}
	leaf, err := newLeaf(caDER, caKey)
	if err != nil {
		return err
	}
	c.leaf = leaf
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	f, err := os.CreateTemp("", "sapi-test-ca-*.pem")
	if err != nil {
		return err
	}
	if _, err := f.Write(pemBytes); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Load this CA before the first TLS handshake. crypto/x509 reads
	// SSL_CERT_FILE once, from the environment, on Linux.
	if err := os.Setenv("SSL_CERT_FILE", f.Name()); err != nil {
		return err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	c.proxyURL = "http://" + ln.Addr().String()
	if err := os.Setenv("HTTPS_PROXY", c.proxyURL); err != nil {
		return err
	}
	// net/http caches ProxyFromEnvironment on first use. Prime it now so a
	// later test cannot freeze an empty proxy.
	req, err := http.NewRequest(http.MethodGet, sapiDevBinaryURL, nil)
	if err != nil {
		return err
	}
	if _, err := http.ProxyFromEnvironment(req); err != nil {
		return err
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go c.serve(conn)
		}
	}()
	return nil
}

func (c *cdnStandIn) set(files map[string][]byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.files = files
	c.paths = nil
	c.handshakeErr = ""
}

func (c *cdnStandIn) snapshot() (paths []string, handshakeErr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.paths...), c.handshakeErr
}

func (c *cdnStandIn) body(path string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paths = append(c.paths, path)
	b, ok := c.files[path]
	if !ok {
		return nil, false
	}
	return append([]byte(nil), b...), true
}

func (c *cdnStandIn) noteHandshake(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	c.handshakeErr = err.Error()
	c.mu.Unlock()
}

func (c *cdnStandIn) serve(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	hdr, err := readHTTPHeader(conn)
	if err != nil {
		return
	}
	line, _, _ := bytes.Cut(hdr, []byte("\r\n"))
	fields := strings.Fields(string(line))
	if len(fields) < 2 || fields[0] != http.MethodConnect || fields[1] != "cdn.blazium.online:443" {
		_, _ = conn.Write([]byte("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n"))
		return
	}
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	tlsConn := tls.Server(conn, &tls.Config{
		Certificates: []tls.Certificate{c.leaf},
		NextProtos:   []string{"http/1.1"},
		MinVersion:   tls.VersionTLS12,
	})
	if err := tlsConn.Handshake(); err != nil {
		c.noteHandshake(err)
		return
	}
	br := bufio.NewReader(tlsConn)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
		body, ok := c.body(req.URL.Path)
		status := http.StatusOK
		if !ok {
			status = http.StatusNotFound
			body = nil
		}
		if err := writeHTTP(tlsConn, status, body); err != nil {
			return
		}
	}
}

func readHTTPHeader(conn net.Conn) ([]byte, error) {
	var buf []byte
	tmp := make([]byte, 1)
	for {
		n, err := conn.Read(tmp)
		if n == 1 {
			buf = append(buf, tmp[0])
			if bytes.HasSuffix(buf, []byte("\r\n\r\n")) {
				return buf, nil
			}
			if len(buf) > 8192 {
				return nil, fmt.Errorf("proxy header too large")
			}
		}
		if err != nil {
			return nil, err
		}
	}
}

func writeHTTP(conn net.Conn, status int, body []byte) error {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "HTTP/1.1 %d %s\r\nContent-Length: %d\r\nConnection: keep-alive\r\n\r\n", status, http.StatusText(status), len(body))
	buf.Write(body)
	_, err := conn.Write(buf.Bytes())
	return err
}

func newTestCA() ([]byte, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "sapi-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	return der, key, nil
}

func newLeaf(caDER []byte, caKey *ecdsa.PrivateKey) (tls.Certificate, error) {
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return tls.Certificate{}, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "cdn.blazium.online"},
		DNSNames:     []string{"cdn.blazium.online"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, nil
}

func requireLinuxAmd64(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("games_sapi is downloaded and executed on linux/amd64")
	}
}

func isolateSapiEmulator(t *testing.T) (cacheRoot, packDir string) {
	t.Helper()
	requireLinuxAmd64(t)
	cacheRoot = t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SAPI_BINARY", "")
	t.Setenv("BLAZIUM_SECRET_KEY", "placeholder")
	t.Setenv("BLAZIUM_ACCESS_TOKEN", "placeholder")
	packDir = filepath.Join(t.TempDir(), "pack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "hello.luau"), []byte("return {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return cacheRoot, packDir
}

func cachedSapiBinary(root string) string {
	return filepath.Join(root, "chauffeur", "games_sapi")
}

func emulatorScript(marker string) []byte {
	return []byte("#!/bin/sh\n{\nprintf '%s\\n' ran\nif [ -n \"$BLAZIUM_SECRET_KEY\" ]; then printf '%s\\n' secret=set; else printf '%s\\n' secret=unset; fi\nif [ -n \"$BLAZIUM_ACCESS_TOKEN\" ]; then printf '%s\\n' token=set; else printf '%s\\n' token=unset; fi\n} > " + strconv.Quote(marker) + "\n")
}

func seedEmulator(t *testing.T, path string, payload []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o700); err != nil {
		t.Fatal(err)
	}
}

func readMarker(path string) (bool, string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, ""
	}
	return true, string(b)
}

func sapiObjectPaths(t *testing.T) (binPath, sumPath string) {
	t.Helper()
	u, err := url.Parse(sapiDevBinaryURL)
	if err != nil || u.Scheme != "https" || u.Host != "cdn.blazium.online" || u.Path == "" {
		t.Fatalf("emulator URL %q", sapiDevBinaryURL)
	}
	return u.Path, u.Path + ".sha256"
}

func callSapiDev(t *testing.T, dir string) error {
	t.Helper()
	return withSapiGlobals(t, dir, "free", "", 8787, func() error {
		return sapiDevCmd.RunE(sapiDevCmd, nil)
	})
}

func withSapiGlobals(t *testing.T, dir, plan, binary string, port int, fn func() error) error {
	t.Helper()
	prevDir, prevPlan, prevBin, prevUsers := sapiDir, sapiPlan, sapiBinary, sapiUsers
	prevPort := sapiPort
	t.Cleanup(func() {
		sapiDir, sapiPlan, sapiBinary, sapiUsers = prevDir, prevPlan, prevBin, prevUsers
		sapiPort = prevPort
	})
	sapiDir, sapiPlan, sapiBinary, sapiUsers = dir, plan, binary, ""
	sapiPort = port
	return fn()
}

func transportFailure(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for _, frag := range []string{"tls:", "x509:", "proxyconnect", "connection refused", "no such host", "i/o timeout", "handshake", "download failed:", "EOF", "connection reset"} {
		if strings.Contains(s, frag) {
			return true
		}
	}
	return false
}

func cacheHolds(path string, payload []byte) bool {
	b, err := os.ReadFile(path)
	return err == nil && bytes.Equal(b, payload)
}

func assertTempsGone(t *testing.T, cache string) {
	t.Helper()
	for _, suffix := range []string{".download", ".sha256"} {
		p := cache + suffix
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("left %s behind", p)
		}
	}
}

// TestSapiDevRefusesUnpinnedCDNEmulator serves a replacement emulator and a
// checksum of those same bytes. The client must not save or run them: the
// checksum is published beside the binary, so it does not authenticate it.
func TestSapiDevRefusesUnpinnedCDNEmulator(t *testing.T) {
	root, pack := isolateSapiEmulator(t)
	marker := filepath.Join(t.TempDir(), "ran")
	payload := emulatorScript(marker)
	sum := sha256.Sum256(payload)
	binPath, sumPath := sapiObjectPaths(t)
	sapiCDN.set(map[string][]byte{
		binPath: payload,
		sumPath: []byte(hex.EncodeToString(sum[:]) + "  games_sapi\n"),
	})
	t.Cleanup(func() { sapiCDN.set(nil) })

	cache := cachedSapiBinary(root)
	err := callSapiDev(t, pack)
	ran, text := readMarker(marker)
	installed := cacheHolds(cache, payload)
	paths, handshake := sapiCDN.snapshot()
	if ran || installed || err == nil {
		t.Fatalf("unpinned emulator from %s was accepted: err=%v installed=%v cdn=%v handshake=%s marker=%q", sapiDevBinaryURL, err, installed, paths, handshake, text)
	}
	if len(paths) == 0 && transportFailure(err) {
		t.Fatalf("stand-in CDN was not reached (handshake=%s): %v", handshake, err)
	}
	assertTempsGone(t, cache)
}

// TestSapiDownloadRejectsChecksumMismatch is the self-check that already
// exists. A replacement that does not match the published checksum must not
// be saved or run, and the refusal must not leave the download behind.
func TestSapiDownloadRejectsChecksumMismatch(t *testing.T) {
	root, pack := isolateSapiEmulator(t)
	marker := filepath.Join(t.TempDir(), "ran")
	payload := emulatorScript(marker)
	sum := sha256.Sum256(payload)
	published := strings.Repeat("ab", 32)
	if hex.EncodeToString(sum[:]) == published {
		t.Fatal("fixture hash collided with the mismatched checksum")
	}
	binPath, sumPath := sapiObjectPaths(t)
	sapiCDN.set(map[string][]byte{
		binPath: payload,
		sumPath: []byte(published + "\n"),
	})
	t.Cleanup(func() { sapiCDN.set(nil) })

	cache := cachedSapiBinary(root)
	err := callSapiDev(t, pack)
	ran, text := readMarker(marker)
	if ran || cacheHolds(cache, payload) {
		t.Fatalf("mismatched emulator was saved or run: err=%v marker=%q", err, text)
	}
	if err == nil {
		t.Fatal("accepted a checksum that does not match the emulator")
	}
	paths, handshake := sapiCDN.snapshot()
	if len(paths) == 0 && transportFailure(err) {
		t.Fatalf("stand-in CDN was not reached (handshake=%s): %v", handshake, err)
	}
	assertTempsGone(t, cache)
}

// TestSapiCommandsRefuseCachedUnpinnedEmulator covers every command that
// executes a previously saved emulator. The stand-in answers 404, so a pass
// cannot depend on downloading the object again.
func TestSapiCommandsRefuseCachedUnpinnedEmulator(t *testing.T) {
	commands := []struct {
		name string
		run  func(t *testing.T, dir string) error
	}{
		{"dev", func(t *testing.T, dir string) error { return callSapiDev(t, dir) }},
		{"test", func(t *testing.T, dir string) error {
			return withSapiGlobals(t, dir, "free", "", 8787, func() error {
				return sapiTestCmd.RunE(sapiTestCmd, nil)
			})
		}},
		{"validate", func(t *testing.T, dir string) error {
			return withSapiGlobals(t, dir, "free", "", 8787, func() error {
				return sapiValidateCmd.RunE(sapiValidateCmd, nil)
			})
		}},
	}
	for _, tc := range commands {
		t.Run(tc.name, func(t *testing.T) {
			root, pack := isolateSapiEmulator(t)
			marker := filepath.Join(t.TempDir(), "ran")
			cache := cachedSapiBinary(root)
			seedEmulator(t, cache, emulatorScript(marker))
			sapiCDN.set(nil)
			before, _ := sapiCDN.snapshot()
			err := tc.run(t, pack)
			after, _ := sapiCDN.snapshot()
			ran, text := readMarker(marker)
			if ran || err == nil {
				t.Fatalf("cached unpinned emulator ran on sapi %s without a new download (cdn requests %d -> %d): err=%v marker=%q", tc.name, len(before), len(after), err, text)
			}
		})
	}
}

// TestSapiRunRefusesEmulatorSwappedAfterResolve resolves the cached path, then
// replaces the file before execution. Checking the path earlier and executing
// whatever is there afterwards still runs the replacement.
func TestSapiRunRefusesEmulatorSwappedAfterResolve(t *testing.T) {
	root, _ := isolateSapiEmulator(t)
	markerA := filepath.Join(t.TempDir(), "a")
	markerB := filepath.Join(t.TempDir(), "b")
	cache := cachedSapiBinary(root)
	seedEmulator(t, cache, emulatorScript(markerA))
	sapiCDN.set(nil)

	bin, err := findSapiBinary("", true)
	if err == nil && bin != cache {
		t.Fatalf("resolved %s, cache is %s", bin, cache)
	}
	seedEmulator(t, cache, emulatorScript(markerB))
	runErr := runSapiBinary(cache, nil, nil)
	ranA, textA := readMarker(markerA)
	ranB, textB := readMarker(markerB)
	if ranA || ranB || runErr == nil {
		t.Fatalf("executed emulator bytes that were not the bytes just checked: findErr=%v runErr=%v markerA=%q markerB=%q", err, runErr, textA, textB)
	}
}
