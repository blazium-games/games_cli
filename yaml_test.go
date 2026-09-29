package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "spec.yml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseBuildWithMediaSymbolsAndEngine(t *testing.T) {
	p := writeYAML(t, `version: v1
spec: build
asset:
  title: My Game
  type: game
  description: A game
  version: 1.2.0
  engine_version: v4.3
  platforms:
    - os: Darwin
      arch: amd64
    - os: windows
  symbols: build/symbols
  media:
    cover: art/cover.png
    thumbnail: art/thumb.png
    gallery: [a.png, b.png]
`)
	cfg, err := ParseYAML(p)
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.BuildAsset
	if a.EngineVersion != "4.3" || a.Symbols != "build/symbols" || a.Media.Cover != "art/cover.png" || len(a.Media.Gallery) != 2 {
		t.Fatalf("asset = %+v", a)
	}
	if a.Platforms[0] != (PlatformSpec{OS: "macos", Arch: "x86_64", Channel: "stable"}) || a.Platforms[1].Arch != "x86_64" {
		t.Fatalf("platforms = %+v", a.Platforms)
	}
}

func TestParseBuildRejectsBadValues(t *testing.T) {
	base := "version: v1\nspec: build\nasset:\n  title: T\n  type: game\n  description: D\n  version: '1'\n"
	for name, extra := range map[string]string{
		"os":      "  platforms:\n    - os: amiga\n",
		"engine":  "  engine_version: latest\n",
		"video":   "  video: javascript:alert(1)\n",
		"gallery": "  media:\n    gallery: [" + strings.Repeat("a.png,", 21) + "]\n",
	} {
		if _, err := ParseYAML(writeYAML(t, base+extra)); exitCode(err) != exitUsage {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

func TestAssetTypeIsDeprecatedAlias(t *testing.T) {
	var buf bytes.Buffer
	old := stderr
	stderr = &buf
	defer func() { stderr = old }()
	cfg, err := ParseYAML(writeYAML(t, `version: v1
spec: addfiles
asset:
  asset_type: game
  version: 1.0.0
  channel: Beta
  os: linux
  arch: aarch64
  engine_version: 4.3.1
  symbols: game.sym
  files:
    - file: game.x86_64
`))
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.FilesAsset
	if a.Type != "game" || a.Channel != "beta" || a.Arch != "arm64" || a.EngineVersion != "4.3.1" || a.Symbols != "game.sym" {
		t.Fatalf("asset = %+v", a)
	}
	if !strings.Contains(buf.String(), "deprecated") {
		t.Fatal("asset_type must print a deprecation warning")
	}
}
