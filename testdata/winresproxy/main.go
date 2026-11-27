// Proxy and checksum database for the Windows release-tool test.
//
// The publisher of github.com/tc-hib/go-winres can cut a new release. This
// process serves that release the way proxy.golang.org and sum.golang.org do:
// @latest is the highest release, and the checksum database records the hash
// of the zip the publisher uploaded. A later exact version that this publisher
// did not serve is absent.
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
	"golang.org/x/mod/sumdb"
	"golang.org/x/mod/sumdb/dirhash"
)

const (
	modPath = "github.com/tc-hib/go-winres"

	// Test keys from the Go toolchain's proxy test
	// (src/cmd/go/proxy_test.go). They are not a production trust root.
	sumDBSigner = "PRIVATE+KEY+localhost.localdev/sumdb+00000c67+AXu6+oaVaOYuQOFrf1V59JK1owcFlJcHwwXHDfDGxSPk"
)

type release struct {
	version string
	modFile []byte
	zip     []byte
	zipHash string
	modHash string
	info    []byte
}

func main() {
	versions := os.Args[1:]
	if len(versions) == 0 {
		versions = []string{"v0.9.9"}
	}
	releases := map[string]release{}
	var latest release
	for _, vers := range versions {
		rel, err := publisherRelease(vers)
		if err != nil {
			log.Fatal(err)
		}
		releases[vers] = rel
		if latest.version == "" || semver.Compare(vers, latest.version) > 0 {
			latest = rel
		}
	}

	ops := sumdb.NewTestServer(sumDBSigner, func(path, vers string) ([]byte, error) {
		rel, ok := releases[vers]
		if path != modPath || !ok {
			return nil, os.ErrNotExist
		}
		return []byte(fmt.Sprintf("%s %s %s\n%s %s/go.mod %s\n", path, vers, rel.zipHash, path, vers, rel.modHash)), nil
	})
	sumSrv := sumdb.NewServer(ops)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("http://%s\n", ln.Addr().String())
	os.Stdout.Sync()

	err = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/latest" || strings.HasPrefix(r.URL.Path, "/lookup/") || strings.HasPrefix(r.URL.Path, "/tile/"):
			sumSrv.ServeHTTP(w, r)
		default:
			serveModule(w, r, releases, latest)
		}
	}))
	log.Fatal(err)
}

func serveModule(w http.ResponseWriter, r *http.Request, releases map[string]release, latest release) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	switch {
	case path == modPath+"/@latest":
		w.Write(latest.info)
	case path == modPath+"/@v/list":
		for vers := range releases {
			fmt.Fprintf(w, "%s\n", vers)
		}
	default:
		const prefix = modPath + "/@v/"
		if !strings.HasPrefix(path, prefix) {
			http.NotFound(w, r)
			return
		}
		file := strings.TrimPrefix(path, prefix)
		ver, ext, ok := splitVerExt(file)
		if !ok {
			http.NotFound(w, r)
			return
		}
		rel, found := releases[ver]
		if !found {
			// A branch or other non-version query resolves to the publisher's
			// current release. An exact version this publisher did not cut is absent.
			if ext == "info" && module.CanonicalVersion(ver) != ver {
				w.Write(latest.info)
				return
			}
			http.NotFound(w, r)
			return
		}
		switch ext {
		case "info":
			w.Write(rel.info)
		case "mod":
			w.Write(rel.modFile)
		case "zip":
			w.Write(rel.zip)
		default:
			http.NotFound(w, r)
		}
	}
}

func splitVerExt(file string) (string, string, bool) {
	for _, ext := range []string{"info", "mod", "zip"} {
		suffix := "." + ext
		if strings.HasSuffix(file, suffix) {
			return strings.TrimSuffix(file, suffix), ext, true
		}
	}
	return "", "", false
}

func publisherRelease(version string) (release, error) {
	modFile := []byte("module github.com/tc-hib/go-winres\n\ngo 1.21\n")
	mainFile := []byte(attackerMain)
	rel := release{version: version, modFile: modFile}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range map[string][]byte{
		modPath + "@" + version + "/go.mod":  modFile,
		modPath + "@" + version + "/main.go": mainFile,
	} {
		f, createErr := zw.Create(name)
		if createErr != nil {
			return release{}, createErr
		}
		if _, createErr = f.Write(data); createErr != nil {
			return release{}, createErr
		}
	}
	if err := zw.Close(); err != nil {
		return release{}, err
	}
	rel.zip = buf.Bytes()
	var err error
	rel.zipHash, err = hashZipBytes(rel.zip)
	if err != nil {
		return release{}, err
	}
	rel.modHash, err = dirhash.Hash1([]string{"go.mod"}, func(string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(modFile)), nil
	})
	if err != nil {
		return release{}, err
	}
	rel.info, err = json.Marshal(struct {
		Version string
		Time    time.Time
	}{Version: version, Time: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		return release{}, err
	}
	return rel, nil
}

func hashZipBytes(zipBytes []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return "", err
	}
	var names []string
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return "", err
		}
		names = append(names, f.Name)
		files[f.Name] = data
	}
	return dirhash.Hash1(names, func(name string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(files[name])), nil
	})
}

// attackerMain is the program a new release of the module runs when the
// packaging step executes it. It reads the workspace environment file and
// replaces the upload helper the next workflow step runs.
const attackerMain = `package main

import (
	"os"
)

func main() {
	if b, err := os.ReadFile(".env"); err == nil {
		if err := os.WriteFile(".release-tool-saw-env", b, 0o644); err != nil {
			os.Exit(1)
		}
	}
	if err := os.MkdirAll("dist/host", 0o755); err != nil {
		os.Exit(1)
	}
	script := "#!/bin/sh\nset -eu\nprev=\nfile=\nfor arg in \"$@\"; do\n  if [ \"$prev\" = \"--file\" ]; then\n    file=$arg\n  fi\n  prev=$arg\ndone\nprintf '%s\\n' \"$*\" > .release-tool-upload-args\nif [ -n \"$file\" ]; then\n  printf '%s' 'attacker-controlled-archive' > \"$file\"\nfi\n"
	if err := os.WriteFile("dist/host/upload", []byte(script), 0o755); err != nil {
		os.Exit(1)
	}
}
`
