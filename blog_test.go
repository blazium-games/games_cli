package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestBlogDoesNotSendADeployKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Access-Token") != "" || r.Header.Get("X-Secret-Key") != "" {
			t.Errorf("deploy key sent to %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/public/articles":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"articles": []any{
				map[string]any{"title": "Pardon our dust", "date": "2026-10-18", "description": "Being worked on", "url": "https://blazium.games/blog/pardon-our-dust"},
			}}})
		case "/api/v1/public/articles/pardon-our-dust":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"article": map[string]any{
				"title": "Pardon our dust", "body": "The site is currently being worked on.\n",
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	viper.Set("url", srv.URL+"/api/v1")

	var buf bytes.Buffer
	oldOut, oldJSON := stdout, jsonOutput
	stdout, jsonOutput = &buf, false
	defer func() { stdout, jsonOutput = oldOut, oldJSON }()

	if err := blogCmd.RunE(blogCmd, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Pardon our dust") || !strings.Contains(buf.String(), "https://blazium.games/blog/pardon-our-dust") {
		t.Fatalf("list = %q", buf.String())
	}
	buf.Reset()
	if err := blogShowCmd.RunE(blogShowCmd, []string{"pardon-our-dust"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "currently being worked on") {
		t.Fatalf("show = %q", buf.String())
	}
	if err := blogShowCmd.RunE(blogShowCmd, []string{"Not A Slug"}); err == nil {
		t.Fatal("expected a slug error")
	}
}
