package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSapiParseFeatureList(t *testing.T) {
	got, err := sapiParseFeatureList(" Lobby_data, daily ,hub,daily")
	if err != nil || !reflect.DeepEqual(got, []string{"hub", "daily", "lobby_data"}) {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := sapiParseFeatureList("none"); err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
	for _, bad := range []string{"dailyy", "../x", "daily;wheel"} {
		if _, err := sapiParseFeatureList(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestSapiScaffoldWritesHelpersAndTheChosenScripts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pack")
	written, skipped, err := sapiScaffold(dir, []string{"lives"}, false)
	if err != nil || len(skipped) != 0 {
		t.Fatalf("%v %v", skipped, err)
	}
	for _, want := range []string{"lib/router.luau", "lib/guard.luau", "lives.luau", "lives.test.luau", "economy.luau", "economy.test.luau"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(want))); err != nil {
			t.Fatalf("missing %s in %v", want, written)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "daily.luau")); err == nil {
		t.Fatal("wrote a feature nobody asked for")
	}
}

func TestSapiScaffoldKeepsExistingFilesUnlessForced(t *testing.T) {
	dir := t.TempDir()
	mine := filepath.Join(dir, "daily.luau")
	if err := os.WriteFile(mine, []byte("-- mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	written, skipped, err := sapiScaffold(dir, []string{"daily"}, false)
	if err != nil || len(skipped) != 1 || skipped[0] != "daily.luau" {
		t.Fatalf("%v %v %v", written, skipped, err)
	}
	if body, _ := os.ReadFile(mine); string(body) != "-- mine\n" {
		t.Fatal("overwrote a file without --force")
	}
	if _, skipped, err = sapiScaffold(dir, []string{"daily"}, true); err != nil || len(skipped) != 0 {
		t.Fatalf("%v %v", skipped, err)
	}
	if body, _ := os.ReadFile(mine); string(body) == "-- mine\n" {
		t.Fatal("--force did not overwrite")
	}
	if _, _, err := sapiScaffold("", []string{"daily"}, false); err == nil {
		t.Fatal("accepted no directory")
	}
}

func TestSapiFeatureAddRefusesACapability(t *testing.T) {
	old := sapiDir
	sapiDir = t.TempDir()
	defer func() { sapiDir = old }()
	if err := sapiFeatureAddCmd.RunE(sapiFeatureAddCmd, []string{"lobby"}); err == nil || !strings.Contains(err.Error(), "capability") {
		t.Fatalf("%v", err)
	}
	if err := sapiFeatureAddCmd.RunE(sapiFeatureAddCmd, []string{"wheel"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(sapiDir, "wheel.luau")); err != nil {
		t.Fatal(err)
	}
}

// The embedded starter scripts are a copy of games_sapi/templates. This fails when one moves without the other.
func TestEmbeddedTemplatesMatchGamesSapi(t *testing.T) {
	root := filepath.Join("..", "games_sapi", "templates")
	if _, err := os.Stat(root); err != nil {
		t.Skip("games_sapi is not next to games_cli")
	}
	seen := map[string]bool{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, werr error) error {
		if werr != nil || info.IsDir() {
			return werr
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		seen[rel] = true
		want, _ := os.ReadFile(p)
		got, rerr := sapiTemplateFS.ReadFile(sapiTemplateRoot + "/" + rel)
		if rerr != nil {
			t.Errorf("%s is not embedded: copy games_sapi/templates into games_cli/sapi_templates", rel)
			return nil
		}
		if !bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))) {
			t.Errorf("%s differs from games_sapi/templates", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range sapiStarterFeatures {
		if !seen[id+".luau"] || !seen[id+".test.luau"] {
			t.Errorf("games_sapi/templates has no script and test for %s", id)
		}
	}
}

// The id lists and requirements are a copy of games_service's feature_catalog.json.
func TestFeatureListsMatchTheCatalog(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "games_service", "feature_catalog.json"))
	if err != nil {
		t.Skip("games_service is not next to games_cli")
	}
	var doc struct {
		Features []struct {
			ID       string   `json:"id"`
			Requires []string `json:"requires"`
		} `json:"features"`
		Capabilities []struct {
			ID string `json:"id"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var features, caps []string
	requires := map[string][]string{}
	for _, f := range doc.Features {
		features = append(features, f.ID)
		if len(f.Requires) > 0 {
			requires[f.ID] = f.Requires
		}
	}
	for _, c := range doc.Capabilities {
		caps = append(caps, c.ID)
	}
	if !reflect.DeepEqual(features, sapiStarterFeatures) {
		t.Errorf("features differ: catalog %v, chauffeur %v", features, sapiStarterFeatures)
	}
	if !reflect.DeepEqual(caps, sapiCapabilityIDs) {
		t.Errorf("capabilities differ: catalog %v, chauffeur %v", caps, sapiCapabilityIDs)
	}
	if !reflect.DeepEqual(requires, sapiFeatureRequires) {
		t.Errorf("requirements differ: catalog %v, chauffeur %v", requires, sapiFeatureRequires)
	}
}

func TestSapiLogicFeaturesReadsTopLevelFileNames(t *testing.T) {
	got := sapiLogicFeatures([]lobbyFile{{name: "daily.luau"}, {name: "daily.test.luau"}, {name: "lib/economy.luau"}, {name: "Wheel.lua"}, {name: "custom.luau"}})
	if !reflect.DeepEqual(got, []string{"daily", "wheel"}) {
		t.Fatalf("%v", got)
	}
}

func TestSapiDisabledWarnings(t *testing.T) {
	data := map[string]any{
		"managed":    true,
		"enable_url": "https://blazium.games/edit/mygame/sapi",
		"features": []any{
			map[string]any{"id": "daily", "kind": "feature", "enabled": true, "active": true},
			map[string]any{"id": "wheel", "kind": "feature", "enabled": false, "active": false},
			map[string]any{"id": "economy", "kind": "feature", "enabled": true, "active": false, "staff_disabled": true},
		},
	}
	lines := sapiDisabledWarnings(data, []string{"daily", "economy", "wheel"})
	text := strings.Join(lines, "\n")
	for _, want := range []string{"wheel", "economy (stopped by staff)", "https://blazium.games/edit/mygame/sapi"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %q", want, text)
		}
	}
	if strings.Contains(text, "daily") {
		t.Fatalf("warned about a feature that is on: %q", text)
	}
	if got := sapiDisabledWarnings(map[string]any{"managed": false}, []string{"wheel"}); got != nil {
		t.Fatalf("an unmanaged game has every feature on: %v", got)
	}
	data["features"] = []any{map[string]any{"id": "daily", "active": true}}
	if got := sapiDisabledWarnings(data, []string{"daily"}); got != nil {
		t.Fatalf("%v", got)
	}
}

func TestPublishWarnsAboutDisabledFeaturesAndStillSucceeds(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"wheel.luau": "return {}\n", "daily.luau": "return {}\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/tool/sapi/features":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
				"managed": true, "enable_url": "https://blazium.games/edit/g/sapi",
				"features": []any{
					map[string]any{"id": "daily", "enabled": true, "active": true},
					map[string]any{"id": "wheel", "enabled": false, "active": false},
				},
			}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"ok": true, "valid": true}})
		}
	}))
	defer srv.Close()
	var errOut bytes.Buffer
	oldErr := stderr
	stderr = &errOut
	defer func() { stderr = oldErr }()
	if err := runSapiPublish(testClient(srv.URL), dir, "stable", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "wheel") || strings.Contains(errOut.String(), "daily") {
		t.Fatalf("warning was %q", errOut.String())
	}
}

func TestPublishSaysNothingWhenTheFeatureCheckFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "wheel.luau"), []byte("return {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/tool/sapi/features" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"success":false}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"ok": true, "valid": true}})
	}))
	defer srv.Close()
	var errOut bytes.Buffer
	oldErr := stderr
	stderr = &errOut
	defer func() { stderr = oldErr }()
	if err := runSapiPublish(testClient(srv.URL), dir, "stable", false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut.String(), "Warning") {
		t.Fatalf("warned on a failed check: %q", errOut.String())
	}
}
