package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Player text stored by a lobby script is a JSON value. encoding/json escapes
// C0 controls, U+2028 and U+2029, and writes DEL, C1 and bidi controls as the
// raw characters. chauffeur lobby data get and lobby data export (with no
// --out file) print that JSON on the terminal. These tests fail until both
// prints pass the JSON text through termText, the filter chauffeur sapi data
// get and export already use.
//
// lobby data export --out writes a file the developer named and is not a
// terminal print, so it is not covered here. lobby data list and lobby config
// get already reach the terminal through logf, which applies termText.

func playerControlRunes() []rune {
	out := []rune{'\u001b', '\u007f'}
	for r := rune(0x80); r <= 0x9f; r++ {
		out = append(out, r)
	}
	out = append(out, '\u061c', '\u200e', '\u200f', '\u2028', '\u2029')
	for r := rune(0x202a); r <= 0x202e; r++ {
		out = append(out, r)
	}
	for r := rune(0x2066); r <= 0x2069; r++ {
		out = append(out, r)
	}
	return out
}

// playerText is a stored string a player can cause a lobby script to save.
// leaked is every unsafe rune that encoding/json writes back into JSON text.
func playerText(t *testing.T) (text string, leaked []rune) {
	t.Helper()
	var b strings.Builder
	b.WriteString("player")
	for _, r := range playerControlRunes() {
		if !unsafeRune(r) {
			t.Fatalf("U+%04X is not treated as a terminal control", r)
		}
		b.WriteRune(r)
		encoded, err := json.Marshal(string(r))
		if err != nil {
			t.Fatal(err)
		}
		if strings.ContainsRune(string(encoded), r) {
			leaked = append(leaked, r)
		}
	}
	if len(leaked) == 0 {
		t.Fatal("encoding/json escaped every control, so this test would not observe a raw terminal sequence")
	}
	return b.String(), leaked
}

func assertStoredTextInert(t *testing.T, out string, leaked []rune) {
	t.Helper()
	shown := termText(out)
	if !strings.Contains(out, "player") {
		t.Fatalf("stored text was not printed:\n%s", shown)
	}
	if strings.ContainsRune(out, '\u001b') {
		t.Fatalf("stdout contains a raw ESC from stored lobby data:\n%s", shown)
	}
	for _, r := range leaked {
		if strings.ContainsRune(out, r) {
			t.Fatalf("stdout contains raw U+%04X from stored lobby data:\n%s", r, shown)
		}
		esc := fmt.Sprintf(`\u%04x`, r)
		if !strings.Contains(out, esc) {
			t.Fatalf("stdout missing %s for stored U+%04X:\n%s", esc, r, shown)
		}
	}
}

func lobbyDataResponse(t *testing.T, items []map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"success": true,
		"data":    map[string]any{"items": items},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// lobbyDataEscapedResponse is the same document with every non-ASCII and
// control character written as a JSON \uXXXX escape, which is what a store
// returns when it escapes the saved string. json.Unmarshal turns those
// escapes back into the characters before the command prints the value.
func lobbyDataEscapedResponse(value string) []byte {
	var b strings.Builder
	b.WriteString(`{"success":true,"data":{"items":[{"key":"other","value":"clean"},{"key":"chat","value":"`)
	for _, r := range value {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r > 0x7e:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString(`"}]}}`)
	return []byte(b.String())
}

func lobbyDataAPI(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tool/lobby-data/data" && r.URL.Path != "/tool/sapi/data" {
			t.Errorf("path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Access-Token") == "" || r.Header.Get("X-Secret-Key") == "" {
			t.Errorf("deploy key was not sent")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
}

func captureStdout(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	oldOut, oldJSON := stdout, jsonOutput
	stdout = &buf
	t.Cleanup(func() {
		stdout = oldOut
		jsonOutput = oldJSON
	})
	return &buf
}

func useLobbyScope(t *testing.T) {
	t.Helper()
	oldScope, oldChannel, oldUser, oldLobby := lobbyDataScope, lobbyDataChannel, lobbyDataUser, lobbyDataLobby
	oldPrefix, oldOut := lobbyDataPrefix, lobbyDataOut
	lobbyDataScope, lobbyDataChannel, lobbyDataUser, lobbyDataLobby = "global", "", "", ""
	lobbyDataPrefix, lobbyDataOut = "", ""
	t.Cleanup(func() {
		lobbyDataScope, lobbyDataChannel, lobbyDataUser, lobbyDataLobby = oldScope, oldChannel, oldUser, oldLobby
		lobbyDataPrefix, lobbyDataOut = oldPrefix, oldOut
	})
}

func useSapiScope(t *testing.T) {
	t.Helper()
	oldScope, oldChannel, oldUser := sapiScope, sapiChannel, sapiUser
	oldPrefix, oldOut := sapiPrefix, sapiOut
	sapiScope, sapiChannel, sapiUser = "global", "", ""
	sapiPrefix, sapiOut = "", ""
	t.Cleanup(func() {
		sapiScope, sapiChannel, sapiUser = oldScope, oldChannel, oldUser
		sapiPrefix, sapiOut = oldPrefix, oldOut
	})
}

func storedItems(value any) []map[string]any {
	return []map[string]any{
		{"key": "other", "value": "clean"},
		{"key": "chat", "value": value},
	}
}

func TestLobbyDataGetPrintsStoredControls(t *testing.T) {
	text, leaked := playerText(t)
	out := runLobbyDataGet(t, lobbyDataResponse(t, storedItems(text)), false)
	assertStoredTextInert(t, out, leaked)
}

func TestLobbyDataGetPrintsNestedStoredControls(t *testing.T) {
	text, leaked := playerText(t)
	value := map[string]any{"from": text, "also": []any{text}}
	out := runLobbyDataGet(t, lobbyDataResponse(t, storedItems(value)), false)
	assertStoredTextInert(t, out, leaked)
}

func TestLobbyDataGetPrintsControlsDecodedFromJSONEscapes(t *testing.T) {
	text, leaked := playerText(t)
	out := runLobbyDataGet(t, lobbyDataEscapedResponse(text), false)
	assertStoredTextInert(t, out, leaked)
}

func TestLobbyDataExportPrintsStoredControls(t *testing.T) {
	text, leaked := playerText(t)
	out := runLobbyDataExport(t, lobbyDataResponse(t, storedItems(text)))
	assertStoredTextInert(t, out, leaked)
	if !strings.Contains(out, "clean") || !strings.Contains(out, "other") {
		t.Fatalf("export dropped a stored key:\n%s", termText(out))
	}
}

func TestLobbyDataExportPrintsNestedStoredControls(t *testing.T) {
	text, leaked := playerText(t)
	value := map[string]any{text: "name", "note": text}
	out := runLobbyDataExport(t, lobbyDataResponse(t, storedItems(value)))
	assertStoredTextInert(t, out, leaked)
}

func TestLobbyDataExportPrintsControlsDecodedFromJSONEscapes(t *testing.T) {
	text, leaked := playerText(t)
	out := runLobbyDataExport(t, lobbyDataEscapedResponse(text))
	assertStoredTextInert(t, out, leaked)
}

func TestLobbyDataGetJSONFiltersStoredControls(t *testing.T) {
	text, leaked := playerText(t)
	out := runLobbyDataGet(t, lobbyDataResponse(t, storedItems(text)), true)
	assertStoredTextInert(t, out, leaked)
}

func TestLobbyDataListFiltersStoredControls(t *testing.T) {
	text, leaked := playerText(t)
	buf := captureStdout(t)
	jsonOutput = false
	useLobbyScope(t)
	srv := lobbyDataAPI(t, lobbyDataResponse(t, storedItems(text)))
	defer srv.Close()
	cmd := deployCmd(t, map[string]string{
		"BLAZIUM_ACCESS_TOKEN": "test-access",
		"BLAZIUM_SECRET_KEY":   "test-secret",
		"BLAZIUM_API_URL":      srv.URL,
		"BLAZIUM_UPLOAD_URL":   srv.URL,
	})
	if err := lobbyDataListCmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	assertStoredTextInert(t, buf.String(), leaked)
}

func TestSapiDataGetFiltersStoredControls(t *testing.T) {
	text, leaked := playerText(t)
	buf := captureStdout(t)
	jsonOutput = false
	useSapiScope(t)
	srv := lobbyDataAPI(t, lobbyDataResponse(t, storedItems(text)))
	defer srv.Close()
	cmd := deployCmd(t, map[string]string{
		"BLAZIUM_ACCESS_TOKEN": "test-access",
		"BLAZIUM_SECRET_KEY":   "test-secret",
		"BLAZIUM_API_URL":      srv.URL,
		"BLAZIUM_UPLOAD_URL":   srv.URL,
	})
	if err := sapiDataGetCmd.RunE(cmd, []string{"chat"}); err != nil {
		t.Fatal(err)
	}
	assertStoredTextInert(t, buf.String(), leaked)
}

func runLobbyDataGet(t *testing.T, body []byte, asJSON bool) string {
	t.Helper()
	buf := captureStdout(t)
	jsonOutput = asJSON
	useLobbyScope(t)
	srv := lobbyDataAPI(t, body)
	defer srv.Close()
	cmd := deployCmd(t, map[string]string{
		"BLAZIUM_ACCESS_TOKEN": "test-access",
		"BLAZIUM_SECRET_KEY":   "test-secret",
		"BLAZIUM_API_URL":      srv.URL,
		"BLAZIUM_UPLOAD_URL":   srv.URL,
	})
	if err := lobbyDataGetCmd.RunE(cmd, []string{"chat"}); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func runLobbyDataExport(t *testing.T, body []byte) string {
	t.Helper()
	buf := captureStdout(t)
	jsonOutput = false
	useLobbyScope(t)
	srv := lobbyDataAPI(t, body)
	defer srv.Close()
	cmd := deployCmd(t, map[string]string{
		"BLAZIUM_ACCESS_TOKEN": "test-access",
		"BLAZIUM_SECRET_KEY":   "test-secret",
		"BLAZIUM_API_URL":      srv.URL,
		"BLAZIUM_UPLOAD_URL":   srv.URL,
	})
	if err := lobbyDataExportCmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
