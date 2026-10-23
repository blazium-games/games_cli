package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

func TestLobbyPackAcceptsLuauAndSkipsRepoFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/master\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".luaurc"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.luau"), []byte("return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "turn.lua"), []byte("return 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "outside.luau")
	if err := os.Symlink(filepath.Join(dir, "main.luau"), link); err != nil {
		t.Logf("symlink skipped: %v", err)
	}
	files, err := lobbyLuaFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, file := range files {
		got[file.name] = string(file.body)
	}
	if got["main.luau"] != "return 1\n" || got["scripts/turn.lua"] != "return 2\n" || len(got) != 2 {
		t.Fatalf("packed %#v", got)
	}
	blob, err := storedZip(files)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, file := range reader.File {
		body, readErr := readZipAll(file)
		if readErr != nil {
			t.Fatal(readErr)
		}
		names[file.Name] = body
	}
	if names["main.luau"] != "return 1\n" || names["scripts/turn.lua"] != "return 2\n" {
		t.Fatalf("zip %#v", names)
	}
}

func readZipAll(file *zip.File) (string, error) {
	rc, err := file.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(rc); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func TestLobbyTypeIsRelayOrScripted(t *testing.T) {
	if _, err := normalizeLobbyType("mygame"); err == nil {
		t.Fatal("expected an invalid lobby type")
	}
	got, err := normalizeLobbyType("Scripted")
	if err != nil || got != "scripted" {
		t.Fatalf("%q %v", got, err)
	}
	body, err := lobbySetBody("", "relay", "")
	if err != nil || body["lobby_type"] != "relay" {
		t.Fatalf("%v %v", body, err)
	}
	if _, err := lobbySetBody("", "mygame", ""); err == nil {
		t.Fatal("lobby set accepted a bad type")
	}
	if !strings.Contains(lobbyPublishCmd.Flags().Lookup("type").Usage, "relay or scripted") {
		t.Fatal(lobbyPublishCmd.Flags().Lookup("type").Usage)
	}
}
