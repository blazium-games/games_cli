package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// playerBugMessage is the text a player files. It mixes a readable report
// with the sequences the list command must not pass through to the terminal:
// every ASCII control (including ESC, CR, LF, and DEL) and an OSC 52
// clipboard sequence followed by a screen-clear.
func playerBugMessage() string {
	var controls strings.Builder
	for b := byte(0); b < 0x20; b++ {
		controls.WriteByte(b)
	}
	controls.WriteByte(0x7f)
	return "BEFORE café " + controls.String() + "\x1b]52;c;aWQgbm93\x07\x1b[2J\x1b[H" + "AFTER"
}

func printableSegments(s string) []string {
	var segs []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			segs = append(segs, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			flush()
			continue
		}
		b.WriteRune(r)
	}
	flush()
	return segs
}

// assertTerminalSafe is the text-mode contract for chauffeur bugs: one line
// per ticket, no ASCII control from a ticket field, and every printable run
// of the player message still visible. Stripping controls and escaping them
// both satisfy it.
func assertTerminalSafe(t *testing.T, out string, rows int, message string) {
	t.Helper()
	if strings.Count(out, "\n") != rows {
		t.Fatalf("newlines = %d, want %d row-ending newlines\n%q", strings.Count(out, "\n"), rows, out)
	}
	for i, r := range out {
		if r == '\n' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			t.Fatalf("ASCII control %#U at byte %d in %q", r, i, out)
		}
	}
	for _, seg := range printableSegments(message) {
		if !strings.Contains(out, seg) {
			t.Fatalf("printable segment %q missing from %q", seg, out)
		}
	}
}

func bugListServer(t *testing.T, message string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/tool/bugs" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Secret-Key") != testSecret || r.Header.Get("X-Access-Token") != "tok" {
			t.Errorf("deploy key headers missing on %s", r.URL.Path)
		}
		data := map[string]any{
			"bugs": []any{map[string]any{
				"uid":     testBugUID,
				"status":  "open",
				"message": message,
			}},
		}
		if r.URL.Query().Get("page") == "1" {
			data["next_page"] = 2
		}
		w.Header().Set("Content-Type", "application/json")
		raw, err := json.Marshal(map[string]any{"success": true, "data": data})
		if err != nil {
			t.Errorf("marshal fixture: %v", err)
			return
		}
		if bytes.Contains(raw, []byte{0x1b}) {
			t.Errorf("fixture wrote a raw ESC: %s", raw)
		}
		if strings.Contains(message, "\x1b") && !bytes.Contains(raw, []byte(`\u001b`)) {
			t.Errorf("fixture did not JSON-escape ESC: %s", raw)
		}
		_, _ = w.Write(raw)
	}))
}

func runBugsList(t *testing.T, apiURL string, asJSON bool, page, status string) string {
	t.Helper()
	deployCmd(t, map[string]string{
		"BLAZIUM_ACCESS_TOKEN": "tok",
		"BLAZIUM_SECRET_KEY":   testSecret,
		"BLAZIUM_API_URL":      apiURL + "/api/v1",
	})
	var buf bytes.Buffer
	oldOut, oldJSON := stdout, jsonOutput
	stdout, jsonOutput = &buf, asJSON
	t.Cleanup(func() {
		stdout, jsonOutput = oldOut, oldJSON
		_ = bugsCmd.Flags().Set("page", "0")
		_ = bugsCmd.Flags().Set("status", "")
	})
	if err := bugsCmd.Flags().Set("page", page); err != nil {
		t.Fatal(err)
	}
	if err := bugsCmd.Flags().Set("status", status); err != nil {
		t.Fatal(err)
	}
	if err := bugsCmd.RunE(bugsCmd, nil); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestBugListTextDoesNotEmitPlayerControls(t *testing.T) {
	message := playerBugMessage()
	srv := bugListServer(t, message)
	defer srv.Close()

	t.Run("every page", func(t *testing.T) {
		out := runBugsList(t, srv.URL, false, "0", "open")
		assertTerminalSafe(t, out, 2, message)
	})
	t.Run("one page", func(t *testing.T) {
		out := runBugsList(t, srv.URL, false, "1", "")
		assertTerminalSafe(t, out, 1, message)
	})
}

func TestBugListJSONDoesNotEmitRawControls(t *testing.T) {
	message := playerBugMessage()
	srv := bugListServer(t, message)
	defer srv.Close()

	out := runBugsList(t, srv.URL, true, "0", "")
	for i, b := range []byte(out) {
		// Indent newlines are the encoder's. Every other C0 byte, including ESC,
		// has to stay inside a JSON escape.
		if b < 0x20 && b != '\n' {
			t.Fatalf("raw control %#x at %d in JSON output %q", b, i, out)
		}
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(err)
	}
	bugs, _ := parsed["bugs"].([]any)
	if len(bugs) != 2 {
		t.Fatalf("bugs = %#v", parsed["bugs"])
	}
	row, _ := bugs[0].(map[string]any)
	if row["message"] != message {
		t.Fatalf("json message = %#v", row["message"])
	}
}
