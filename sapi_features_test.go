package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSapiFeaturePath(t *testing.T) {
	got, err := sapiFeaturePath(" Daily ", "disable")
	if err != nil || got != "/tool/sapi/features/daily/disable" {
		t.Fatalf("%q %v", got, err)
	}
	got, err = sapiFeaturePath("lobby_data", "enable")
	if err != nil || got != "/tool/sapi/features/lobby_data/enable" {
		t.Fatalf("%q %v", got, err)
	}
	for _, bad := range []string{"", "../x", "a b", "9lives", strings.Repeat("a", 70)} {
		if _, err := sapiFeaturePath(bad, "enable"); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestPrintSapiFeatures(t *testing.T) {
	var out bytes.Buffer
	old := stdout
	stdout = &out
	defer func() { stdout = old }()
	printSapiFeatures(map[string]any{
		"managed": true,
		"features": []any{
			map[string]any{"id": "daily", "kind": "feature", "enabled": true},
			map[string]any{"id": "economy", "kind": "feature", "sensitive": true},
			map[string]any{"id": "lobby", "kind": "capability", "plan_ok": false, "min_plan": "premium", "sensitive": true},
			map[string]any{"id": "wheel", "kind": "feature", "enabled": true, "staff_disabled": true},
		},
	})
	text := out.String()
	for _, want := range []string{"daily", "on", "sensitive", "needs premium", "stopped by staff"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Feature management is off") {
		t.Fatal("a managed game should not print the legacy note")
	}
}
