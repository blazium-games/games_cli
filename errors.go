package main

import (
	"errors"
	"fmt"
)

// Exit codes: 0 success, 1 usage or validation error, 2 API error, 3 network
// error after retries.
const (
	exitOK      = 0
	exitUsage   = 1
	exitAPI     = 2
	exitNetwork = 3
)

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usageErrorf(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

// apiError is a response the service rejected, parsed from the
// {success, data, error{code, message}} envelope.
type apiError struct {
	Status  int
	Code    int
	Message string
	Data    map[string]any
}

func (e *apiError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = fmt.Sprintf("HTTP %d", e.Status)
	}
	if e.Code != 0 {
		msg = fmt.Sprintf("API error [%d]: %s", e.Code, msg)
	} else {
		msg = "API error: " + msg
	}
	if h := codeHint(e.Code); h != "" {
		msg += "\n  hint: " + h
	}
	return msg
}

// retryable reports whether trying again may succeed: rate limits and server errors.
func (e *apiError) retryable() bool {
	return e.Status == 429 || e.Status >= 500
}

type networkError struct{ err error }

func (e *networkError) Error() string { return "network error: " + e.err.Error() }
func (e *networkError) Unwrap() error { return e.err }

var codeHints = map[int]string{
	4025: "the deploy key was rejected. Check BLAZIUM_ACCESS_TOKEN and BLAZIUM_SECRET_KEY, or issue a new key on the dashboard (this invalidates the old one).",
	4026: "a build field is missing or too long: version (32), title (255), description (10000), changelog (100 entries), demo_url (http/https, 255), engine_version (like 4.3).",
	4037: "the upload body could not be read; the file field must come after the form fields.",
	4038: "the build could not be identified; pass the build_id printed by chauffeur build.",
	4039: "the build was not found for this deploy key's game.",
	4041: "os, arch, channel, checksum or filename is invalid. os: " + osHelp + "; arch: " + archHelp + "; build files must be .zip.",
	4043: "the file is too large (5 GB per build file, 512 MB per symbol file).",
	4044: "the chunk does not continue the upload session; chauffeur resumes from the server's position.",
	4045: "the upload session expired or was not found; run the command again.",
	4046: "the checksum does not match the uploaded bytes; the file may have changed during upload.",
	4049: "symbol files must be Breakpad .sym files with a MODULE header, or a .zip of them.",
	4096: "the game owner must verify their email on blazium.games before uploading.",
	4150: "the media request is invalid; kind must be cover, thumbnail or gallery.",
	4151: "images must be PNG, JPEG, GIF or WebP, 512-2048 px per side and at most 10 MB.",
	4152: "the gallery is full (20 images); delete one with chauffeur media delete first.",
	4153: "a public listing needs a cover and thumbnail; replace it instead of deleting.",
	4154: "the order must list every gallery image uid exactly once (see chauffeur media list).",
	4166: "a filter is invalid. os: " + osHelp + "; arch: " + archHelp + ".",
	4290: "rate limited; wait a while and try again.",
	4291: "too many open upload sessions for this game; wait for one to finish or expire.",
}

func codeHint(code int) string { return codeHints[code] }

func exitCode(err error) int {
	if err == nil {
		return exitOK
	}
	var ue *usageError
	var ae *apiError
	var ne *networkError
	switch {
	case errors.As(err, &ae):
		return exitAPI
	case errors.As(err, &ne):
		return exitNetwork
	case errors.As(err, &ue):
		return exitUsage
	}
	return exitUsage
}
