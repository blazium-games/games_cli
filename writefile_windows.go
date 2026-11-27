//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// openNoFollow opens path for writing without following a symlink or
// junction at path: FILE_FLAG_OPEN_REPARSE_POINT opens the link itself, which
// is then refused.
func openNoFollow(path string, perm os.FileMode) (*os.File, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(name,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
		nil,
		syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL|syscall.FILE_FLAG_OPEN_REPARSE_POINT,
		0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &info); err != nil {
		syscall.CloseHandle(h)
		return nil, &os.PathError{Op: "stat", Path: path, Err: err}
	}
	if info.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		syscall.CloseHandle(h)
		return nil, fmt.Errorf("refusing to write through symlink %s", path)
	}
	if info.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY != 0 {
		syscall.CloseHandle(h)
		return nil, fmt.Errorf("refusing to write %s: not a regular file", path)
	}
	if info.NumberOfLinks != 1 {
		syscall.CloseHandle(h)
		return nil, fmt.Errorf("refusing to write %s: it has more than one link", path)
	}
	if ft, err := syscall.GetFileType(h); err != nil || ft != syscall.FILE_TYPE_DISK {
		syscall.CloseHandle(h)
		return nil, fmt.Errorf("refusing to write %s: not a regular file", path)
	}
	return os.NewFile(uintptr(h), path), nil
}
