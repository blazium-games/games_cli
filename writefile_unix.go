//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"
)

// openNoFollow opens path write-only without following a final symlink.
// O_NONBLOCK keeps a FIFO at path from blocking the open; the handle is then
// refused because it is not a regular file.
func openNoFollow(path string, perm os.FileMode) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, perm)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !st.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("refusing to write %s: not a regular file", path)
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); !ok || uint64(sys.Nlink) != 1 {
		f.Close()
		return nil, fmt.Errorf("refusing to write %s: it has more than one link", path)
	}
	return f, nil
}
