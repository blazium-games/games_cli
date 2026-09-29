package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPlatformBuildReusesMatchingBuild(t *testing.T) {
	registered := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tool/builds":
			q := r.URL.Query()
			if q.Get("os") != "windows" || q.Get("arch") != "x86_64" || q.Get("channel") != "stable" {
				t.Errorf("unexpected filter %s", r.URL.RawQuery)
			}
			builds := []any{}
			if q.Get("version") == "1.0.0" {
				builds = []any{
					map[string]any{"build_id": "other-type", "app_id": "app", "version": "1.0.0", "build_type": "demo", "os": "windows", "arch": "x86_64", "channel": "stable"},
					map[string]any{"build_id": "keep-me", "app_id": "app", "version": "1.0.0", "build_type": "game", "os": "windows", "arch": "x86_64", "channel": "stable"},
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"builds": builds}})
		case "/tool/upload/build":
			registered++
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"build_id": "new", "app_id": "app"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "tok", "sekrit")
	p := PlatformSpec{OS: "windows", Arch: "x86_64", Channel: "stable"}

	data, err := platformBuild(c, &FilesAsset{Type: "game", Version: "1.0.0"}, p)
	if err != nil {
		t.Fatal(err)
	}
	if got := responseBuildID(data); got != "keep-me" || registered != 0 {
		t.Fatalf("build_id = %q, registered = %d; want the existing build and no registration", got, registered)
	}

	data, err = platformBuild(c, &FilesAsset{Type: "game", Version: "2.0.0"}, p)
	if err != nil {
		t.Fatal(err)
	}
	if got := responseBuildID(data); got != "new" || registered != 1 {
		t.Fatalf("build_id = %q, registered = %d; want a new registration", got, registered)
	}
}
