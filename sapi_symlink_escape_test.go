package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// These tests are the spec for writes that follow a symlink out of the directory
// the developer named. They fail while scaffolding and pack extract still create
// or truncate files at the link target.
//
// The final path is looked up with Lstat before the write. That lookup does not
// catch an intermediate directory symlink when the file itself is absent, and with
// --force it does not stop a symlink at the final path. A fix that looks the same
// lexical path up again, or that only rejects ".." in the template name, still fails
// these tests. Creating or deleting a file outside the selected directory, or
// removing the planted symlink, is a failure even when the call also returns an error.

func TestSymlinkEscapeSapiInitLibDir(t *testing.T) {
	pack, outside := scaffoldTree(t)
	link := filepath.Join("..", filepath.Base(outside))
	plantDirLink(t, pack, "lib", link)
	before := dirSnapshot(t, outside)

	chdir(t, pack)
	withSapiGlobals(t, ".", "hub", false)
	err := sapiInitCmd.RunE(sapiInitCmd, nil)

	assertSnapshotUnchanged(t, outside, before, err)
	assertStillLink(t, filepath.Join(pack, "lib"), link)
}

func TestSymlinkEscapeSapiFeatureAddChainedLib(t *testing.T) {
	pack, outside := scaffoldTree(t)
	// lib points at hop, and hop points outside. The direct link text is a name
	// inside the pack, so a check of that text does not see the escape.
	hopTarget := filepath.Join("..", filepath.Base(outside))
	plantDirLink(t, pack, "hop", hopTarget)
	plantDirLink(t, pack, "lib", "hop")
	before := dirSnapshot(t, outside)

	withSapiGlobals(t, pack, "", false)
	err := sapiFeatureAddCmd.RunE(sapiFeatureAddCmd, []string{"hub"})

	assertSnapshotUnchanged(t, outside, before, err)
	assertStillLink(t, filepath.Join(pack, "lib"), "hop")
	assertStillLink(t, filepath.Join(pack, "hop"), hopTarget)
}

func TestSymlinkEscapeSapiInitForceExistingFile(t *testing.T) {
	pack, outside := scaffoldTree(t)
	link := filepath.Join("..", filepath.Base(outside))
	plantDirLink(t, pack, "lib", link)
	if err := os.WriteFile(filepath.Join(outside, "guard.luau"), []byte("sentinel-keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := dirSnapshot(t, outside)

	withSapiGlobals(t, pack, "hub", true)
	err := sapiInitCmd.RunE(sapiInitCmd, nil)

	assertSnapshotUnchanged(t, outside, before, err)
	assertStillLink(t, filepath.Join(pack, "lib"), link)
}

func TestSymlinkEscapeSapiFeatureAddForceFinalFile(t *testing.T) {
	pack, outside := scaffoldTree(t)
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("sentinel-keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(pack, secret)
	if err != nil {
		t.Fatal(err)
	}
	plantDirLink(t, pack, "hub.luau", rel)
	before := dirSnapshot(t, outside)

	withSapiGlobals(t, pack, "", true)
	err = sapiFeatureAddCmd.RunE(sapiFeatureAddCmd, []string{"hub"})

	assertSnapshotUnchanged(t, outside, before, err)
	assertStillLink(t, filepath.Join(pack, "hub.luau"), rel)
}

func TestSymlinkEscapeSapiInitDanglingLibDir(t *testing.T) {
	base := t.TempDir()
	pack := filepath.Join(base, "repo")
	if err := os.MkdirAll(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	// The link target does not exist yet. MkdirAll must not create it outside the pack.
	link := filepath.Join("..", "missing-outside")
	plantDirLink(t, pack, "lib", link)
	before := dirNames(t, base)

	withSapiGlobals(t, pack, "hub", false)
	err := sapiInitCmd.RunE(sapiInitCmd, nil)

	after := dirNames(t, base)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("dangling lib symlink changed the parent of the pack (call error: %v)\nbefore: %v\nafter:  %v", err, before, after)
	}
	assertStillLink(t, filepath.Join(pack, "lib"), link)
	if _, statErr := os.Lstat(filepath.Join(base, "missing-outside")); !os.IsNotExist(statErr) {
		t.Fatalf("dangling lib symlink materialized outside the pack: %v", statErr)
	}
}

func TestSymlinkEscapeLibLinkInsidePackStillWrites(t *testing.T) {
	pack := filepath.Join(t.TempDir(), "repo")
	inner := filepath.Join(pack, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	plantDirLink(t, pack, "lib", "inner")

	withSapiGlobals(t, pack, "hub", false)
	if err := sapiInitCmd.RunE(sapiInitCmd, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(pack, "lib", "guard.luau"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := sapiTemplateFS.ReadFile(sapiTemplateRoot + "/lib/guard.luau")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("starter script was not written through a link that stays inside the pack")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(pack, "lib", "guard.luau"))
	if err != nil {
		t.Fatal(err)
	}
	if !pathInside(t, pack, resolved) {
		t.Fatalf("resolved %s outside %s", resolved, pack)
	}
}

func TestSymlinkEscapeFinalLinkInsidePackStillWritesWithForce(t *testing.T) {
	pack := filepath.Join(t.TempDir(), "repo")
	inner := filepath.Join(pack, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(pack, filepath.Join(inner, "hub.luau"))
	if err != nil {
		t.Fatal(err)
	}
	plantDirLink(t, pack, "hub.luau", rel)

	withSapiGlobals(t, pack, "hub", true)
	if err := sapiInitCmd.RunE(sapiInitCmd, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(pack, "hub.luau"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := sapiTemplateFS.ReadFile(sapiTemplateRoot + "/hub.luau")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("--force did not write through a final link that stays inside the pack")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(pack, "hub.luau"))
	if err != nil {
		t.Fatal(err)
	}
	if !pathInside(t, pack, resolved) {
		t.Fatalf("resolved %s outside %s", resolved, pack)
	}
}

func TestSymlinkEscapeExtractPackLibDir(t *testing.T) {
	pack, outside := scaffoldTree(t)
	link := filepath.Join("..", filepath.Base(outside))
	plantDirLink(t, pack, "lib", link)
	before := dirSnapshot(t, outside)
	zipPath := writeTestZip(t, map[string]string{
		"daily.luau":      "return 1\n",
		"lib/router.luau": "return 2\n",
	})

	_, err := extractSapiPack(zipPath, pack, false)

	assertSnapshotUnchanged(t, outside, before, err)
	assertStillLink(t, filepath.Join(pack, "lib"), link)
}

func TestSymlinkEscapeExtractPackChainedLib(t *testing.T) {
	pack, outside := scaffoldTree(t)
	hopTarget := filepath.Join("..", filepath.Base(outside))
	plantDirLink(t, pack, "hop", hopTarget)
	plantDirLink(t, pack, "lib", "hop")
	before := dirSnapshot(t, outside)
	zipPath := writeTestZip(t, map[string]string{"lib/router.luau": "return 2\n"})

	_, err := extractSapiPack(zipPath, pack, false)

	assertSnapshotUnchanged(t, outside, before, err)
	assertStillLink(t, filepath.Join(pack, "lib"), "hop")
	assertStillLink(t, filepath.Join(pack, "hop"), hopTarget)
}

func TestSymlinkEscapeExtractPackForceFinalFile(t *testing.T) {
	pack, outside := scaffoldTree(t)
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("sentinel-keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(pack, secret)
	if err != nil {
		t.Fatal(err)
	}
	plantDirLink(t, pack, "daily.luau", rel)
	before := dirSnapshot(t, outside)
	zipPath := writeTestZip(t, map[string]string{"daily.luau": "return 1\n"})

	_, err = extractSapiPack(zipPath, pack, true)

	assertSnapshotUnchanged(t, outside, before, err)
	assertStillLink(t, filepath.Join(pack, "daily.luau"), rel)
}

func TestSymlinkEscapeExtractPackInsideLinkStillWrites(t *testing.T) {
	pack := filepath.Join(t.TempDir(), "repo")
	inner := filepath.Join(pack, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	plantDirLink(t, pack, "lib", "inner")
	zipPath := writeTestZip(t, map[string]string{"lib/router.luau": "return 2\n"})

	if _, err := extractSapiPack(zipPath, pack, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(pack, "lib", "router.luau"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "return 2\n" {
		t.Fatalf("body = %q", got)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(pack, "lib", "router.luau"))
	if err != nil {
		t.Fatal(err)
	}
	if !pathInside(t, pack, resolved) {
		t.Fatalf("resolved %s outside %s", resolved, pack)
	}
}

func scaffoldTree(t *testing.T) (pack, outside string) {
	t.Helper()
	base := t.TempDir()
	pack = filepath.Join(base, "repo")
	outside = filepath.Join(base, "outside")
	if err := os.MkdirAll(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "keep.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return pack, outside
}

func plantDirLink(t *testing.T, dir, name, target string) {
	t.Helper()
	if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
}

func withSapiGlobals(t *testing.T, dir, features string, force bool) {
	t.Helper()
	oldDir, oldFeat, oldForce, oldJSON := sapiDir, sapiInitFeatures, sapiInitForce, jsonOutput
	sapiDir, sapiInitFeatures, sapiInitForce, jsonOutput = dir, features, force, false
	t.Cleanup(func() {
		sapiDir, sapiInitFeatures, sapiInitForce, jsonOutput = oldDir, oldFeat, oldForce, oldJSON
	})
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatal(err)
		}
	})
}

func dirNames(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func dirSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			out[rel] = "symlink:" + target
			return nil
		}
		if d.IsDir() {
			out[rel] = "dir"
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = "file:" + string(body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertSnapshotUnchanged(t *testing.T, root string, before map[string]string, callErr error) {
	t.Helper()
	after := dirSnapshot(t, root)
	if reflect.DeepEqual(before, after) {
		return
	}
	var changed []string
	seen := map[string]bool{}
	for name, body := range before {
		seen[name] = true
		if after[name] != body {
			changed = append(changed, name)
		}
	}
	for name := range after {
		if !seen[name] {
			changed = append(changed, name+" (created)")
		}
	}
	sort.Strings(changed)
	t.Fatalf("write escaped the selected directory (call error: %v): %s", callErr, strings.Join(changed, ", "))
}

func assertStillLink(t *testing.T, path, wantTarget string) {
	t.Helper()
	st, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("path %s was removed: %v", path, err)
	}
	if st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink %s was replaced with mode %v", path, st.Mode())
	}
	got, err := os.Readlink(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != wantTarget {
		t.Fatalf("symlink %s target = %q, want %q", path, got, wantTarget)
	}
}

func pathInside(t *testing.T, root, path string) bool {
	t.Helper()
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(rootResolved, path)
	if err != nil {
		t.Fatal(err)
	}
	return rel != ".." && !filepath.IsAbs(rel) && !hasDotDotPrefix(rel)
}

func hasDotDotPrefix(rel string) bool {
	return rel == ".." || len(rel) >= 3 && rel[0] == '.' && rel[1] == '.' && (rel[2] == '/' || rel[2] == '\\')
}
