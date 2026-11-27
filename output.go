package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// jsonOutput is the global --json flag: one JSON object on stdout, logs on stderr.
var jsonOutput bool

var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
)

// logw is where progress goes: stderr with --json, otherwise stdout.
func logw() io.Writer {
	if jsonOutput {
		return stderr
	}
	return stdout
}

// logf and logln pass every argument through termText. Only the format
// string, which is ours, may carry control characters to the terminal.
func logf(format string, args ...any) { fmt.Fprintf(logw(), format, termArgs(args)...) }

func logln(args ...any) { fmt.Fprintln(logw(), termArgs(args)...) }

type termArg struct{ v any }

func (a termArg) Format(f fmt.State, verb rune) {
	_, _ = io.WriteString(f, termText(fmt.Sprintf(fmt.FormatString(f, verb), a.v)))
}

func termArgs(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = termArg{a}
	}
	return out
}

// termSafe makes text from the server, which may come from players, inert on
// a terminal: control characters (C0, DEL, C1), bidi overrides and line
// separators become \uXXXX, and bytes that are not UTF-8 become \xNN.
// Newlines and tabs are escaped too, so one value prints on one line.
func termSafe(s string) string { return escapeControls(s, false) }

// termText is termSafe for multi-line text: it keeps newlines, tabs, and a
// carriage return that ends a line.
func termText(s string) string { return escapeControls(s, true) }

func escapeControls(s string, keepLines bool) string {
	i := 0
	for i < len(s) {
		r, n := utf8.DecodeRuneInString(s[i:])
		if (r == utf8.RuneError && n == 1) || (unsafeRune(r) && !(keepLines && layoutRune(s, i, r))) {
			break
		}
		i += n
	}
	if i == len(s) {
		return s
	}
	var b strings.Builder
	b.WriteString(s[:i])
	for i < len(s) {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case unsafeRune(r) && !(keepLines && layoutRune(s, i, r)):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}

func layoutRune(s string, i int, r rune) bool {
	return r == '\n' || r == '\t' || (r == '\r' && i+1 < len(s) && s[i+1] == '\n')
}

func unsafeRune(r rune) bool {
	switch {
	case r < 0x20, r >= 0x7f && r <= 0x9f:
		return true
	case r == 0x061c, r == 0x200e, r == 0x200f, r == 0x2028, r == 0x2029,
		r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

// emit prints the command's result object when --json is set. encoding/json
// escapes C0 controls but not DEL, C1 or bidi controls; those can only sit
// inside JSON strings, where termText's \uXXXX is the same character.
func emit(v any) error {
	if !jsonOutput {
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	_, err := io.WriteString(stdout, termText(buf.String()))
	return err
}

// emitError prints {"ok": false, "error": ...} for --json callers.
func emitError(err error) {
	if !jsonOutput || err == nil {
		return
	}
	out := map[string]any{"ok": false, "exit_code": exitCode(err), "error": err.Error()}
	var ae *apiError
	if errors.As(err, &ae) {
		out["code"] = ae.Code
		out["status"] = ae.Status
		if h := codeHint(ae.Status, ae.Code); h != "" {
			out["hint"] = h
		}
	}
	_ = emit(out)
}
