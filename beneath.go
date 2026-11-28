package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// A beneathDir is a directory the CLI writes named files into, such as a pack
// a developer pulls or scaffolds. It is opened once, and every file below it is
// reached from that handle without following a symlink at any step, so a
// symlink already inside the directory cannot send a write somewhere else.
// Each method takes a slash-separated path relative to the directory.

// splitBeneath splits rel into the directories leading to it and its final
// name. Empty and "." elements are dropped; ".." and absolute paths are refused.
func splitBeneath(rel string) (dirs []string, base string, err error) {
	slashed := filepath.ToSlash(rel)
	if strings.HasPrefix(slashed, "/") || filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return nil, "", fmt.Errorf("refusing to write %q: it is not a relative path", rel)
	}
	var parts []string
	for _, p := range strings.Split(slashed, "/") {
		switch p {
		case "", ".":
			continue
		case "..":
			return nil, "", fmt.Errorf("refusing to write %q: it leaves the directory", rel)
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return nil, "", fmt.Errorf("refusing to write %q: it names no file", rel)
	}
	return parts[:len(parts)-1], parts[len(parts)-1], nil
}

func errBeneathSymlink(dir, rel, name string) error {
	return fmt.Errorf("refusing to write %s in %s: %s is a symlink", rel, dir, name)
}

func errBeneathChanged(dir, rel, name string) error {
	return fmt.Errorf("refusing to write %s in %s: %s changed while it was being written", rel, dir, name)
}
