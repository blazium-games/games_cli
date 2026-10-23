package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestCodeHint4039FollowsStatus(t *testing.T) {
	if got := codeHint(http.StatusNotFound, 4039); got != "the build was not found for this deploy key's game." {
		t.Fatalf("404 hint %q", got)
	}
	if got := codeHint(http.StatusForbidden, 4039); !strings.Contains(got, "suspended") {
		t.Fatalf("403 hint %q", got)
	}
	suspended := &apiError{Status: http.StatusForbidden, Code: 4039, Message: "owner is suspended"}
	if strings.Contains(suspended.Error(), "build was not found") {
		t.Fatal(suspended.Error())
	}
	if got := codeHint(http.StatusForbidden, 4512); !strings.Contains(got, "moderation") {
		t.Fatalf("4512 hint %q", got)
	}
}

func TestHelpDoesNotClaimRetriesForEveryCommand(t *testing.T) {
	if strings.Contains(rootCmd.Long, "error after retries") {
		t.Fatal(rootCmd.Long)
	}
	if !strings.Contains(rootCmd.Long, "uploads retry first") {
		t.Fatal(rootCmd.Long)
	}
}

func TestUnknownCommandRespectsJSON(t *testing.T) {
	if !wantsJSON([]string{"bogus", "--json"}) {
		t.Fatal("did not see --json")
	}
	oldOut, oldErr, oldJSON := stdout, stderr, jsonOutput
	t.Cleanup(func() { stdout, stderr, jsonOutput = oldOut, oldErr, oldJSON })
	var out, errBuf bytes.Buffer
	stdout, stderr = &out, &errBuf
	jsonOutput = wantsJSON([]string{"bogus", "--json"})
	reportCommandError(&usageError{msg: `unknown command "bogus" for "chauffeur"`})
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("stdout %q stderr %q: %v", out.String(), errBuf.String(), err)
	}
	if payload["ok"] != false || errBuf.Len() != 0 {
		t.Fatalf("payload %#v stderr %q", payload, errBuf.String())
	}
}

func TestPatronPathAndQueryAreEscaped(t *testing.T) {
	if got := patronResource("/private/patrons", "a/b"); got != "/private/patrons/a%2Fb" {
		t.Fatal(got)
	}
	if got := patronQuery("/private/patron/status", "developer", "a b&c"); got != "/private/patron/status?developer=a+b%26c" {
		t.Fatal(got)
	}
	if got := patronQuery("/private/patron/status", "developer", ""); got != "/private/patron/status" {
		t.Fatal(got)
	}
}

func TestTierUpdateRejectsIgnoredValues(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("name", "", "")
	cmd.Flags().Int64("price-cents", 0, "")
	cmd.Flags().String("active", "", "")
	if err := cmd.Flags().Set("price-cents", "30"); err != nil {
		t.Fatal(err)
	}
	if _, err := tierUpdateBody(cmd); err == nil {
		t.Fatal("price below 50 was accepted")
	}
	cmd = &cobra.Command{}
	cmd.Flags().String("name", "", "")
	cmd.Flags().Int64("price-cents", 0, "")
	cmd.Flags().String("active", "", "")
	if err := cmd.Flags().Set("active", "yes"); err != nil {
		t.Fatal(err)
	}
	if _, err := tierUpdateBody(cmd); err == nil {
		t.Fatal("bad --active was accepted")
	}
	cmd = &cobra.Command{}
	cmd.Flags().String("name", "", "")
	cmd.Flags().Int64("price-cents", 0, "")
	cmd.Flags().String("active", "", "")
	if err := cmd.Flags().Set("price-cents", "500"); err != nil {
		t.Fatal(err)
	}
	body, err := tierUpdateBody(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if body["price_cents"] != int64(500) || len(body) != 1 {
		t.Fatalf("%#v", body)
	}
}

func TestPostUpdateSendsOnlyChangedFields(t *testing.T) {
	cmd := &cobra.Command{}
	postWriteFlags(cmd)
	if err := cmd.Flags().Set("title", "New title"); err != nil {
		t.Fatal(err)
	}
	body, err := postUpdateBody(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if body["title"] != "New title" || len(body) != 1 {
		t.Fatalf("%#v", body)
	}
	if _, ok := body["status"]; ok {
		t.Fatal("update resent status")
	}
	if _, ok := body["visibility"]; ok {
		t.Fatal("update resent visibility")
	}
	if _, ok := body["body"]; ok {
		t.Fatal("update blanked the body")
	}
}

func TestPatronHTTPClientTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Second)
	}))
	defer srv.Close()
	oldClient, oldAPI := patronHTTPClient, apiURL
	t.Cleanup(func() {
		patronHTTPClient = oldClient
		apiURL = oldAPI
	})
	apiURL = srv.URL
	patronHTTPClient = func() *http.Client {
		client := newHTTPClient()
		client.Timeout = 50 * time.Millisecond
		return client
	}
	if newHTTPClient().Timeout == 0 {
		t.Fatal("shared client has no timeout")
	}
	cmd := &cobra.Command{}
	cmd.Flags().String("user-token", "tok", "")
	err := printUserAPI(cmd, http.MethodGet, "/private/patrons", nil)
	var ne *networkError
	if err == nil || !errorsAsNetwork(err, &ne) {
		t.Fatalf("expected a network timeout, got %v", err)
	}
}

func errorsAsNetwork(err error, target **networkError) bool {
	ne, ok := err.(*networkError)
	if ok {
		*target = ne
	}
	return ok
}
