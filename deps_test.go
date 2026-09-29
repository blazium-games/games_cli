package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// chauffeur is a standalone public tool; it must never depend on the private
// shared service module.
var forbiddenModule = "games_" + "common"

func TestNoSharedServiceModule(t *testing.T) {
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && (info.Name() == "vendor" || info.Name() == "dist" || strings.HasPrefix(info.Name(), ".")) && path != "." {
			return filepath.SkipDir
		}
		if info.IsDir() || !(strings.HasSuffix(path, ".go") || path == "go.mod" || path == "go.sum") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), forbiddenModule) {
			t.Errorf("%s references %s", path, forbiddenModule)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not on PATH")
	}
	out, err := exec.Command("go", "list", "-deps", "./...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	if strings.Contains(string(out), forbiddenModule) {
		t.Fatalf("the build depends on %s", forbiddenModule)
	}
}
