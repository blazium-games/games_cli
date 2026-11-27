package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLobbySliceScopes(t *testing.T) {
	s, err := newLobbySlice("lobby", "", "", "room-1")
	if err != nil || s.lobby != "room-1" {
		t.Fatalf("%+v %v", s, err)
	}
	if got := s.listPath("a", "", 500); got != "/tool/lobby-data/data?limit=200&lobby=room-1&prefix=a&scope=lobby" {
		t.Fatal(got)
	}
	if _, err := newLobbySlice("lobby", "", "", ""); err == nil {
		t.Fatal("lobby scope without --lobby")
	}
	if _, err := newLobbySlice("lobby", "beta", "", "room-1"); err == nil {
		t.Fatal("lobby scope with a channel")
	}
	if _, err := newLobbySlice("lobby", "", "u-1", "room-1"); err == nil {
		t.Fatal("lobby scope with a user")
	}
	if _, err := newLobbySlice("global", "", "", "room-1"); err == nil {
		t.Fatal("--lobby on a global scope")
	}
	if _, err := newLobbySlice("lobby", "", "", "../x"); err == nil {
		t.Fatal("accepted a bad lobby id")
	}
	g, err := newLobbySlice("user", "beta", "u-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := g.path(lobbyDataBase+"/data/key", map[string][]string{"key": {"a b"}}); got != "/tool/lobby-data/data/key?channel=beta&key=a+b&scope=user&user=u-1" {
		t.Fatal(got)
	}
}

func TestLobbyResetPath(t *testing.T) {
	got, err := lobbyResetPath("lobby", "", "", "room-1", "p/", "game-uid", true)
	if err != nil || got != "/tool/lobby-data/data?confirm=game-uid&dry_run=true&lobby=room-1&prefix=p%2F&scope=lobby" {
		t.Fatalf("%q %v", got, err)
	}
	got, err = lobbyResetPath("channel", "beta", "", "", "", "game-uid", false)
	if err != nil || !strings.HasPrefix(got, "/tool/lobby-data/data?") || !strings.Contains(got, "scope=channel") || !strings.Contains(got, "confirm=game-uid") {
		t.Fatalf("%q %v", got, err)
	}
	for name, args := range map[string][]string{
		"no scope":         {"", "", "", "", "", "game-uid"},
		"lobby without id": {"lobby", "", "", "", "", "game-uid"},
		"no confirm":       {"lobby", "", "", "room-1", "", ""},
		"lobby on global":  {"global", "", "", "room-1", "", "game-uid"},
		"lobby with user":  {"lobby", "", "u-1", "room-1", "", "game-uid"},
	} {
		if _, err := lobbyResetPath(args[0], args[1], args[2], args[3], args[4], args[5], false); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestLobbyExportFollowsPages(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Path+"?"+r.URL.Query().Get("lobby")+":"+r.URL.Query().Get("after"))
		w.Header().Set("Content-Type", "application/json")
		data := map[string]any{"items": []any{map[string]any{"key": "a", "value": 1}}, "next": "a"}
		if r.URL.Query().Get("after") == "a" {
			data = map[string]any{"items": []any{map[string]any{"key": "b", "value": "two"}}, "next": nil}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
	}))
	defer srv.Close()
	s, _ := newLobbySlice("lobby", "", "", "room-1")
	items, err := exportLobbySlice(testClient(srv.URL), s, "")
	if err != nil || len(items) != 2 || items[1].Key != "b" {
		t.Fatalf("%v %v", items, err)
	}
	if strings.Join(queries, ",") != "/tool/lobby-data/data?room-1:,/tool/lobby-data/data?room-1:a" {
		t.Fatal(queries)
	}
}
