package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLobbySetBody(t *testing.T) {
	body, err := lobbySetBody("true", "scripted", "false")
	if err != nil {
		t.Fatal(err)
	}
	if body["enabled"] != true || body["lobby_type"] != "scripted" || body["ice_enabled"] != false {
		t.Fatalf("%v", body)
	}
	if _, err := lobbySetBody("", "", ""); err == nil {
		t.Fatal("expected a missing flag error")
	}
	if _, err := lobbySetBody("maybe", "", ""); err == nil {
		t.Fatal("expected a bool error")
	}
}

func TestLobbyPackRefusesAngelScript(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old.as"), []byte("void main(){}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := lobbyLuaFiles(dir); err == nil {
		t.Fatal("expected .as to be refused before upload")
	}
}

func TestLobbyPackStoredZipIsLua(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.lua"), []byte("return true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := lobbyLuaFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := storedZip(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(blob) < 4 || blob[0] != 'P' || blob[1] != 'K' {
		t.Fatalf("not a zip: %d bytes", len(blob))
	}
}
