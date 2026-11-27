package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestAuditPath(t *testing.T) {
	got, err := auditPath("/tool/sapi", 0, "")
	if err != nil || got != "/tool/sapi/audit" {
		t.Fatalf("%q %v", got, err)
	}
	got, err = auditPath("/tool/lobby-data", 25, "900")
	if err != nil || got != "/tool/lobby-data/audit?before=900&limit=25" {
		t.Fatalf("%q %v", got, err)
	}
	for _, bad := range []struct {
		limit  int
		before string
	}{{201, ""}, {-1, ""}, {0, "abc"}, {0, "0"}, {0, "1&x=2"}} {
		if _, err := auditPath("/tool/sapi", bad.limit, bad.before); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func capture(t *testing.T) (*bytes.Buffer, func()) {
	var out bytes.Buffer
	old := stdout
	stdout = &out
	return &out, func() { stdout = old }
}

func TestPrintAnalyticsAndAudit(t *testing.T) {
	out, restore := capture(t)
	defer restore()
	printSapiAnalytics(map[string]any{
		"plan": "free", "since": "2026-01-01",
		"totals": map[string]any{"calls": 1200.0, "errors": 3.0, "refused": 7.0, "quota_rejects": 4.0, "avg_ms": 4.5, "p95_ms": 20.0},
		"logics": []any{map[string]any{"logic": "daily", "calls": 900.0, "errors": 1.0, "refused": 2.0}},
		"daily":  []any{map[string]any{"day": "2026-01-02", "calls": 100.0, "errors": 0.0, "refused": 0.0}},
	})
	for _, want := range []string{"plan free", "calls 1200", "refused 7", "quota 4", "avg 4.5 ms", "daily", "2026-01-02"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, out.String())
		}
	}
	out.Reset()
	printLobbyAnalytics(map[string]any{
		"plan": "free", "since": "2026-01-01",
		"limits": map[string]any{"lobbies_per_game": 32.0, "players_per_lobby": 4.0, "lobbies_per_host": 2.0, "turn_allocations": 0.0, "lobby_data_bytes": 4096.0},
		"totals": map[string]any{"lobbies_created": 50.0, "peak_open_lobbies": 12.0, "cap_rejected": 5.0, "lobby_cap_rejected": 3.0, "host_cap_rejected": 1.0, "players_cap_rejected": 1.0},
	})
	for _, want := range []string{"32 open lobbies", "4 players per lobby", "2 per host", "opened 50", "peak open 12", "turned away at the plan's caps 5"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in:\n%s", want, out.String())
		}
	}
	out.Reset()
	printAudit(map[string]any{"entries": []any{map[string]any{"created_at": "t", "actor": "deploy-key", "action": "feature_disable", "detail": "daily"}}, "next_before": 41.0})
	if !strings.Contains(out.String(), "feature_disable") || !strings.Contains(out.String(), "--before 41") {
		t.Fatal(out.String())
	}
	out.Reset()
	printAudit(map[string]any{"entries": []any{}})
	if !strings.Contains(out.String(), "No changes") {
		t.Fatal(out.String())
	}
}

func TestAnalyticsCommandsAreRegistered(t *testing.T) {
	for _, parent := range []string{"sapi", "lobby"} {
		cmd := sapiCmd
		if parent == "lobby" {
			cmd = lobbyCmd
		}
		for _, name := range []string{"analytics", "audit"} {
			found := false
			for _, c := range cmd.Commands() {
				found = found || c.Name() == name
			}
			if !found {
				t.Fatalf("%s %s is not registered", parent, name)
			}
		}
	}
}
