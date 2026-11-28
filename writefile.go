package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// openForOverwrite opens path for writing, creating it with perm if missing,
// and truncates it. A symlink at path is refused rather than followed, and
// nothing is truncated until the opened handle is known to be a regular file
// with a single link.
func openForOverwrite(path string, perm os.FileMode) (*os.File, error) {
	f, err := openNoFollow(path, perm)
	if err != nil {
		if st, lerr := os.Lstat(path); lerr == nil && st.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("refusing to write through symlink %s", path)
		}
		return nil, err
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// writeFileInRoot creates or truncates name under root, making its parent
// directories first. Every component of name, symlinks included, is resolved
// relative to root's open handle, so a link that leads out of root fails the
// write instead of creating or truncating a file elsewhere.
func writeFileInRoot(root *os.Root, name string, body []byte, perm os.FileMode) error {
	if dir := filepath.Dir(name); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("refusing to write %s: %w", name, err)
		}
	}
	if err := root.WriteFile(name, body, perm); err != nil {
		return fmt.Errorf("refusing to write %s: %w", name, err)
	}
	return nil
}

// writeFileNoFollow is os.WriteFile without following a symlink at path.
func writeFileNoFollow(path string, body []byte, perm os.FileMode) error {
	f, err := openForOverwrite(path, perm)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(body)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
