package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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

func logf(format string, args ...any) { fmt.Fprintf(logw(), format, args...) }

func logln(args ...any) { fmt.Fprintln(logw(), args...) }

// emit prints the command's result object when --json is set.
func emit(v any) error {
	if !jsonOutput {
		return nil
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
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
		if h := codeHint(ae.Code); h != "" {
			out["hint"] = h
		}
	}
	_ = emit(out)
}
