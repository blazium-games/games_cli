package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// localFileSentinel is the body of a stand-in for a file outside the work tree.
// Writers must leave it byte for byte as it was.
const localFileSentinel = "local-file-sentinel\n"

func silenceCommandLogs(t *testing.T) {
	t.Helper()
	oldOut, oldErr := stdout, stderr
	stdout, stderr = io.Discard, io.Discard
	t.Cleanup(func() { stdout, stderr = oldOut, oldErr })
}

func writeSentinel(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(localFileSentinel), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertSentinelUntouched(t *testing.T, path string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != localFileSentinel {
		t.Fatalf("symlink target %s = %q, %v; want the original bytes", path, got, err)
	}
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() {
		t.Fatalf("symlink target %s is no longer a regular file: %v", path, err)
	}
}

func assertSymlinkUnchanged(t *testing.T, link, wantTarget string) {
	t.Helper()
	st, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("output path: %v", err)
	}
	if st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("output path mode = %v, want a symlink left in place", st.Mode())
	}
	got, err := os.Readlink(link)
	if err != nil || got != wantTarget {
		t.Fatalf("readlink = %q, %v; want %q", got, err, wantTarget)
	}
}

func lobbyExportServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": map[string]any{
				"items": []any{map[string]any{"key": "coins", "value": "exported"}},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func lobbyExportCommand(t *testing.T, srv *httptest.Server) *cobra.Command {
	t.Helper()
	return deployCmd(t, map[string]string{
		"BLAZIUM_ACCESS_TOKEN": "tok",
		"BLAZIUM_SECRET_KEY":   testSecret,
		"BLAZIUM_API_URL":      srv.URL,
		"BLAZIUM_UPLOAD_URL":   srv.URL,
	})
}

// runLobbyDataExport runs `chauffeur lobby data export --out <out>` through the
// command handler, including flag binding and the authenticated client.
func runLobbyDataExport(t *testing.T, cmd *cobra.Command, out string) error {
	t.Helper()
	if err := lobbyDataExportCmd.Flags().Set("scope", "global"); err != nil {
		t.Fatal(err)
	}
	if err := lobbyDataExportCmd.Flags().Set("channel", ""); err != nil {
		t.Fatal(err)
	}
	if err := lobbyDataExportCmd.Flags().Set("user", ""); err != nil {
		t.Fatal(err)
	}
	if err := lobbyDataExportCmd.Flags().Set("lobby", ""); err != nil {
		t.Fatal(err)
	}
	if err := lobbyDataExportCmd.Flags().Set("prefix", ""); err != nil {
		t.Fatal(err)
	}
	if err := lobbyDataExportCmd.Flags().Set("out", out); err != nil {
		t.Fatal(err)
	}
	return lobbyDataExportCmd.RunE(cmd, nil)
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// TestLobbyDataExportWritesRegularFile is the export a developer asked for:
// --out names a regular file, and that file receives the lobby JSON.
func TestLobbyDataExportWritesRegularFile(t *testing.T) {
	silenceCommandLogs(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "export.json")
	cmd := lobbyExportCommand(t, lobbyExportServer(t))
	if err := runLobbyDataExport(t, cmd, out); err != nil {
		t.Fatal(err)
	}
	st, err := os.Lstat(out)
	if err != nil || !st.Mode().IsRegular() {
		t.Fatalf("export output: mode=%v err=%v", st, err)
	}
	body, err := os.ReadFile(out)
	if err != nil || !strings.Contains(string(body), `"coins"`) || !strings.Contains(string(body), `"exported"`) {
		t.Fatalf("export body = %q, %v", body, err)
	}
}

// TestLobbyDataExportRefusesSymlink is the checkout attack: export.json is a
// symlink to a file outside the project, and --out names that symlink.
func TestLobbyDataExportRefusesSymlink(t *testing.T) {
	silenceCommandLogs(t)
	srv := lobbyExportServer(t)
	cmd := lobbyExportCommand(t, srv)
	dir := t.TempDir()
	regular := filepath.Join(dir, "regular.json")
	if err := runLobbyDataExport(t, cmd, regular); err != nil {
		t.Fatal(err)
	}

	secret := filepath.Join(dir, "outside")
	writeSentinel(t, secret)
	link := filepath.Join(dir, "export.json")
	mustSymlink(t, secret, link)
	err := runLobbyDataExport(t, cmd, link)
	if err == nil {
		t.Fatal("lobby data export wrote through a symlink")
	}
	assertSentinelUntouched(t, secret)
	assertSymlinkUnchanged(t, link, secret)
}

// TestLobbyDataExportRefusesRelativeSymlink matches a symlink committed to the
// project: the link text is relative and points outside the work tree.
func TestLobbyDataExportRefusesRelativeSymlink(t *testing.T) {
	silenceCommandLogs(t)
	root := t.TempDir()
	secret := filepath.Join(root, "notes")
	writeSentinel(t, secret)
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(project, "export.json")
	const rel = "../notes"
	mustSymlink(t, rel, link)
	err := runLobbyDataExport(t, lobbyExportCommand(t, lobbyExportServer(t)), link)
	if err == nil {
		t.Fatal("lobby data export wrote through a relative symlink")
	}
	assertSentinelUntouched(t, secret)
	assertSymlinkUnchanged(t, link, rel)
}

// TestLobbyDataExportDoesNotCreateThroughDanglingSymlink refuses a symlink
// whose target does not exist yet. Creating that target is the same follow.
func TestLobbyDataExportDoesNotCreateThroughDanglingSymlink(t *testing.T) {
	silenceCommandLogs(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "missing")
	link := filepath.Join(dir, "export.json")
	mustSymlink(t, target, link)
	err := runLobbyDataExport(t, lobbyExportCommand(t, lobbyExportServer(t)), link)
	if err == nil {
		t.Fatal("lobby data export followed a dangling symlink")
	}
	if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
		t.Fatalf("dangling symlink created %s: %v", target, statErr)
	}
	assertSymlinkUnchanged(t, link, target)
}

// TestExtractSapiPackForceRefusesSymlink covers chauffeur sapi pull --force,
// which writes each pack file with the same create the lobby export uses.
func TestExtractSapiPackForceRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(t.TempDir(), "outside")
	writeSentinel(t, secret)
	link := filepath.Join(dir, "daily.luau")
	mustSymlink(t, secret, link)
	zipPath := writeTestZip(t, map[string]string{"daily.luau": "return 1\n"})
	if _, err := extractSapiPack(zipPath, dir, true); err == nil {
		t.Fatal("sapi pull --force wrote through a symlink")
	}
	assertSentinelUntouched(t, secret)
	assertSymlinkUnchanged(t, link, secret)
}

// TestExtractSapiPackWithoutForceLeavesDifferentSymlinkTarget is the pull that
// does not pass --force. A symlink to different bytes must not be rewritten.
func TestExtractSapiPackWithoutForceLeavesDifferentSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(t.TempDir(), "outside")
	writeSentinel(t, secret)
	link := filepath.Join(dir, "daily.luau")
	mustSymlink(t, secret, link)
	zipPath := writeTestZip(t, map[string]string{"daily.luau": "return 1\n"})
	if _, err := extractSapiPack(zipPath, dir, false); err == nil {
		t.Fatal("sapi pull rewrote a symlink to different bytes")
	}
	assertSentinelUntouched(t, secret)
	assertSymlinkUnchanged(t, link, secret)
}

// TestExtractSapiPackWithoutForceRefusesMatchingSymlink is pull without --force
// when the linked file already holds the pack bytes. The content check then
// has nothing to reject, and the write still has to refuse the symlink.
func TestExtractSapiPackWithoutForceRefusesMatchingSymlink(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(t.TempDir(), "outside")
	const body = "return 1\n"
	if err := os.WriteFile(secret, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "daily.luau")
	mustSymlink(t, secret, link)
	zipPath := writeTestZip(t, map[string]string{"daily.luau": body})
	if _, err := extractSapiPack(zipPath, dir, false); err == nil {
		t.Fatal("sapi pull rewrote a symlink whose target already matched the pack")
	}
	got, err := os.ReadFile(secret)
	if err != nil || string(got) != body {
		t.Fatalf("symlink target = %q, %v", got, err)
	}
	assertSymlinkUnchanged(t, link, secret)
}

// TestExtractSapiPackDoesNotCreateThroughDanglingSymlink is pull without
// --force when the pack path is a dangling symlink. The target must not appear.
func TestExtractSapiPackDoesNotCreateThroughDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "missing.luau")
	link := filepath.Join(dir, "daily.luau")
	mustSymlink(t, target, link)
	zipPath := writeTestZip(t, map[string]string{"daily.luau": "return 1\n"})
	if _, err := extractSapiPack(zipPath, dir, false); err == nil {
		t.Fatal("sapi pull followed a dangling symlink")
	}
	if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
		t.Fatalf("dangling symlink created %s: %v", target, statErr)
	}
	assertSymlinkUnchanged(t, link, target)
}

// TestSapiScaffoldForceRefusesSymlink covers chauffeur sapi init --force and
// chauffeur sapi feature add --force, which share one writer.
func TestSapiScaffoldForceRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(t.TempDir(), "outside")
	writeSentinel(t, secret)
	link := filepath.Join(dir, "daily.luau")
	mustSymlink(t, secret, link)
	if _, _, err := sapiScaffold(dir, []string{"daily"}, true); err == nil {
		t.Fatal("sapi scaffold --force wrote through a symlink")
	}
	assertSentinelUntouched(t, secret)
	assertSymlinkUnchanged(t, link, secret)
}

// TestSapiScaffoldForceDoesNotCreateThroughDanglingSymlink is init/add --force
// when the starter script is a dangling symlink.
func TestSapiScaffoldForceDoesNotCreateThroughDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "missing.luau")
	link := filepath.Join(dir, "daily.luau")
	mustSymlink(t, target, link)
	if _, _, err := sapiScaffold(dir, []string{"daily"}, true); err == nil {
		t.Fatal("sapi scaffold --force followed a dangling symlink")
	}
	if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
		t.Fatalf("dangling symlink created %s: %v", target, statErr)
	}
	assertSymlinkUnchanged(t, link, target)
}

// TestSapiScaffoldWithoutForceLeavesSymlink is init/add without --force.
// An existing symlink is already present, so it must not be followed.
func TestSapiScaffoldWithoutForceLeavesSymlink(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(t.TempDir(), "outside")
	writeSentinel(t, secret)
	link := filepath.Join(dir, "daily.luau")
	mustSymlink(t, secret, link)
	if _, _, err := sapiScaffold(dir, []string{"daily"}, false); err != nil {
		t.Fatal(err)
	}
	assertSentinelUntouched(t, secret)
	assertSymlinkUnchanged(t, link, secret)
}
