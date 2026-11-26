//go:build linux && amd64

package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// A stat of the destination followed by a normal open still follows a symlink
// swapped in between the two. These tests replace a regular file with a
// symlink when the monitored thread enters openat, which is after any check
// and before the kernel opens the path. The constants match linux/seccomp.h
// and the amd64 syscall table.

const (
	sysSeccomp                   = 317
	prSetNoNewPrivs              = 38
	seccompSetModeFilter         = 1
	seccompFilterFlagNewListener = 8
	seccompRetUserNotif          = 0x7fc00000
	seccompRetAllow              = 0x7fff0000
	auditArchX86_64              = 0xC000003E
	seccompIoctlNotifRecv        = 0xC0502100
	seccompIoctlNotifSend        = 0xC0182101
	seccompUserNotifFlagContinue = 1
	sysOpenat                    = 257
)

type sockFilter struct {
	Code uint16
	Jt   uint8
	Jf   uint8
	K    uint32
}

type sockFprog struct {
	Len    uint16
	Filter *sockFilter
}

type seccompData struct {
	Nr                 int32
	Arch               uint32
	InstructionPointer uint64
	Args               [6]uint64
}

type seccompNotif struct {
	ID    uint64
	PID   uint32
	Flags uint32
	Data  seccompData
}

type seccompNotifResp struct {
	ID    uint64
	Val   int64
	Error int32
	Flags uint32
}

func readCString(tid int, addr uint64) (string, error) {
	if addr == 0 {
		return "", fmt.Errorf("empty path pointer")
	}
	f, err := os.Open(fmt.Sprintf("/proc/%d/mem", tid))
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, err := f.ReadAt(buf, int64(addr))
	if n == 0 {
		return "", err
	}
	if i := bytes.IndexByte(buf[:n], 0); i >= 0 {
		return string(buf[:i]), nil
	}
	return string(buf[:n]), nil
}

func pathIsDest(path, dest string) bool {
	if path == dest || filepath.Clean(path) == dest {
		return true
	}
	return !strings.Contains(path, "/") && path == filepath.Base(dest)
}

func plantSymlink(dest, target string) error {
	tmp := dest + ".swap-link"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

// callWhileSwappingSymlinkOnOpen runs fn while a regular file at dest is
// replaced with a symlink to target on the open that uses that path.
func callWhileSwappingSymlinkOnOpen(dest, target string, fn func() error) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write([]byte("decoy-regular-file")); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	var mu sync.Mutex
	var swapErr error
	installed := make(chan int, 1)
	errCh := make(chan error, 1)

	go func() {
		runtime.LockOSThread()
		filters := []sockFilter{
			{Code: 0x20, K: 4},
			{Code: 0x15, Jt: 1, Jf: 0, K: auditArchX86_64},
			{Code: 0x06, K: seccompRetAllow},
			{Code: 0x20, K: 0},
			{Code: 0x15, Jt: 0, Jf: 1, K: sysOpenat},
			{Code: 0x06, K: seccompRetUserNotif},
			{Code: 0x06, K: seccompRetAllow},
		}
		prog := sockFprog{Len: uint16(len(filters)), Filter: &filters[0]}
		if _, _, errno := syscall.RawSyscall6(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0, 0, 0, 0); errno != 0 {
			errCh <- errno
			return
		}
		fd, _, errno := syscall.Syscall(sysSeccomp, seccompSetModeFilter, seccompFilterFlagNewListener, uintptr(unsafe.Pointer(&prog)))
		if errno != 0 {
			errCh <- errno
			return
		}
		installed <- int(fd)
		errCh <- fn()
	}()

	var listener int
	select {
	case listener = <-installed:
	case err := <-errCh:
		return fmt.Errorf("installing the open monitor: %w", err)
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timed out installing the open monitor")
	}

	go func() {
		for {
			var n seccompNotif
			_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(listener), seccompIoctlNotifRecv, uintptr(unsafe.Pointer(&n)))
			if errno != 0 {
				return
			}
			path, _ := readCString(int(n.PID), n.Data.Args[1])
			if pathIsDest(path, dest) {
				if err := plantSymlink(dest, target); err != nil {
					mu.Lock()
					if swapErr == nil {
						swapErr = err
					}
					mu.Unlock()
				}
			}
			resp := seccompNotifResp{ID: n.ID, Flags: seccompUserNotifFlagContinue}
			_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, uintptr(listener), seccompIoctlNotifSend, uintptr(unsafe.Pointer(&resp)))
			if errno != 0 {
				return
			}
		}
	}()

	var fnErr error
	select {
	case fnErr = <-errCh:
	case <-time.After(10 * time.Second):
		syscall.Close(listener)
		return fmt.Errorf("timed out in the write")
	}
	syscall.Close(listener)
	mu.Lock()
	defer mu.Unlock()
	if swapErr != nil {
		return fmt.Errorf("planting the symlink: %w", swapErr)
	}
	return fnErr
}

func requireTargetUntouched(t *testing.T, err error, target string) {
	t.Helper()
	got, readErr := os.ReadFile(target)
	if readErr != nil || string(got) != originalFileBytes {
		t.Fatalf("symlink target = %q, %v (write err=%v)", got, readErr, err)
	}
	if err == nil {
		t.Fatal("write succeeded after the destination was replaced with a symlink")
	}
}

func TestDownloadFileSymlinkSwappedBeforeOpen(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "original-file")
	if err := os.WriteFile(target, []byte(originalFileBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "project", "bugs", "shot.bin")
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		t.Fatal(err)
	}
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(playerShotBytes))
	}))
	t.Cleanup(files.Close)
	err := callWhileSwappingSymlinkOnOpen(dest, target, func() error {
		return testClient("https://api.example").DownloadFile(files.URL+"/shot", dest)
	})
	requireTargetUntouched(t, err, target)
}

func TestWritePrivateFileSymlinkSwappedBeforeOpen(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "original-file")
	if err := os.WriteFile(target, []byte(originalFileBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "project", "bugs", testBugUID+"-log.txt")
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		t.Fatal(err)
	}
	err := callWhileSwappingSymlinkOnOpen(dest, target, func() error {
		return writePrivateFile(dest, []byte(playerLogBytes))
	})
	requireTargetUntouched(t, err, target)
}

func TestPullScreenshotSymlinkSwappedBeforeOpen(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "original-file")
	if err := os.WriteFile(target, []byte(originalFileBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "project", "bugs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, testBugUID+"-screenshot")
	c := newBugDownloadFixture(t, true)
	err := callWhileSwappingSymlinkOnOpen(dest, target, func() error {
		return pullBugFiles(c, dir, testBugUID)
	})
	requireTargetUntouched(t, err, target)
}

func TestPullLogSymlinkSwappedBeforeOpen(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "original-file")
	if err := os.WriteFile(target, []byte(originalFileBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "project", "bugs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, testBugUID+"-log.txt")
	c := newBugDownloadFixture(t, false)
	err := callWhileSwappingSymlinkOnOpen(dest, target, func() error {
		return pullBugFiles(c, dir, testBugUID)
	})
	requireTargetUntouched(t, err, target)
}
