//go:build linux

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/spf13/viper"
	"golang.org/x/sys/unix"
)

// Pack unpacking must not create or rewrite a file outside the directory the
// developer selected. These tests fail while a directory symlink inside that
// directory is followed, or a symlink at the final path is written through.
// They also fail if a fix resolves the path and then writes it with a later
// lookup: the swap test replaces the real directory with a symlink after any
// earlier check and before the creating open runs.

func publisherPack(t *testing.T, files map[string]string) string {
	t.Helper()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	list := make([]lobbyFile, 0, len(names))
	for _, name := range names {
		list = append(list, lobbyFile{name: name, body: []byte(files[name])})
	}
	blob, err := storedZip(list)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pack.zip")
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func treeNames(t *testing.T, root string) []string {
	t.Helper()
	var names []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		names = append(names, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	return names
}

func requireUnchangedTree(t *testing.T, root string, before []string) {
	t.Helper()
	if got := treeNames(t, root); !reflect.DeepEqual(got, before) {
		t.Errorf("wrote outside the selected directory: before %v after %v", before, got)
	}
}

func requireSameSymlink(t *testing.T, link, want string) {
	t.Helper()
	fi, err := os.Lstat(link)
	if err != nil {
		t.Errorf("symlink %s missing: %v", link, err)
		return
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("%s is no longer a symlink (%v)", link, fi.Mode())
		return
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Errorf("readlink %s: %v", link, err)
		return
	}
	if got != want {
		t.Errorf("symlink %s target changed from %q to %q", link, want, got)
	}
}

func linkDir(t *testing.T, dir, name, target string) string {
	t.Helper()
	link := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return link
}

func TestExtractSapiPackDoesNotFollowDirectorySymlink(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	link := linkDir(t, dir, "lib", outside)
	before := treeNames(t, outside)
	zp := publisherPack(t, map[string]string{"lib/guard.luau": "pwned", "daily.luau": "return 1"})
	_, err := extractSapiPack(zp, dir, false)
	requireUnchangedTree(t, outside, before)
	requireSameSymlink(t, link, outside)
	if err == nil {
		t.Error("extract accepted lib/guard.luau through a directory symlink")
	}
}

func TestExtractSapiPackDoesNotCreateDirectoriesThroughSymlink(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	link := linkDir(t, dir, "lib", outside)
	before := treeNames(t, outside)
	zp := publisherPack(t, map[string]string{"lib/nested/guard.luau": "pwned"})
	_, err := extractSapiPack(zp, dir, false)
	requireUnchangedTree(t, outside, before)
	requireSameSymlink(t, link, outside)
	if err == nil {
		t.Error("extract created a directory through a directory symlink")
	}
}

func TestExtractSapiPackDoesNotFollowDeeperDirectorySymlink(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := linkDir(t, dir, filepath.Join("scripts", "lib"), outside)
	before := treeNames(t, outside)
	zp := publisherPack(t, map[string]string{"scripts/lib/guard.lua": "pwned"})
	_, err := extractSapiPack(zp, dir, false)
	requireUnchangedTree(t, outside, before)
	requireSameSymlink(t, link, outside)
	if err == nil {
		t.Error("extract accepted a .lua path through a nested directory symlink")
	}
}

func TestExtractSapiPackDoesNotFollowRelativeDirectorySymlink(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "proj")
	outside := filepath.Join(parent, "outside")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	link := linkDir(t, dir, "lib", "../outside")
	before := treeNames(t, outside)
	zp := publisherPack(t, map[string]string{"lib/guard.luau": "pwned"})
	_, err := extractSapiPack(zp, dir, false)
	requireUnchangedTree(t, outside, before)
	requireSameSymlink(t, link, "../outside")
	if err == nil {
		t.Error("extract followed a relative directory symlink")
	}
}

func TestExtractSapiPackDoesNotRewriteMatchingFileThroughDirectorySymlink(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	link := linkDir(t, dir, "lib", outside)
	dest := filepath.Join(outside, "guard.luau")
	body := []byte("return 1")
	if err := os.WriteFile(dest, body, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(1, 0)
	if err := os.Chtimes(dest, old, old); err != nil {
		t.Fatal(err)
	}
	zp := publisherPack(t, map[string]string{"lib/guard.luau": string(body)})
	_, err := extractSapiPack(zp, dir, false)
	got, rerr := os.ReadFile(dest)
	if rerr != nil || string(got) != string(body) {
		t.Fatalf("target content = %q %v", got, rerr)
	}
	st, serr := os.Stat(dest)
	if serr != nil || !st.ModTime().Equal(old) {
		t.Errorf("rewrote the file through the directory symlink (mtime %v)", st.ModTime())
	}
	requireSameSymlink(t, link, outside)
	if err == nil {
		t.Error("extract accepted a matching file through a directory symlink")
	}
}

func TestExtractSapiPackForceDoesNotOverwriteThroughDirectorySymlink(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	link := linkDir(t, dir, "lib", outside)
	dest := filepath.Join(outside, "guard.luau")
	if err := os.WriteFile(dest, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	zp := publisherPack(t, map[string]string{"lib/guard.luau": "pwned"})
	_, err := extractSapiPack(zp, dir, true)
	got, rerr := os.ReadFile(dest)
	if rerr != nil || string(got) != "keep" {
		t.Fatalf("force overwrote the directory symlink target: %q %v", got, rerr)
	}
	requireSameSymlink(t, link, outside)
	if err == nil {
		t.Error("extract --force accepted a path through a directory symlink")
	}
}

func TestExtractSapiPackForceDoesNotFollowFinalSymlink(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	dest := filepath.Join(outside, "guard.luau")
	if err := os.WriteFile(dest, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := linkDir(t, dir, "guard.luau", dest)
	zp := publisherPack(t, map[string]string{"guard.luau": "pwned"})
	_, err := extractSapiPack(zp, dir, true)
	got, rerr := os.ReadFile(dest)
	if rerr != nil || string(got) != "keep" {
		t.Fatalf("force overwrote the final symlink target: %q %v", got, rerr)
	}
	requireSameSymlink(t, link, dest)
	if err == nil {
		t.Error("extract --force wrote through a final-path symlink")
	}
}

func TestExtractSapiPackForceDoesNotFollowFinalSymlinkInsideDir(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "other.luau")
	if err := os.WriteFile(other, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := linkDir(t, dir, "daily.luau", "other.luau")
	zp := publisherPack(t, map[string]string{"daily.luau": "pwned"})
	_, err := extractSapiPack(zp, dir, true)
	got, rerr := os.ReadFile(other)
	if rerr != nil || string(got) != "keep" {
		t.Fatalf("force overwrote the file the symlink points at: %q %v", got, rerr)
	}
	requireSameSymlink(t, link, "other.luau")
	if err == nil {
		t.Error("extract --force wrote through a final-path symlink inside the directory")
	}
}

func TestExtractSapiPackDoesNotCreateThroughDanglingFinalSymlink(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	target := filepath.Join(outside, "guard.luau")
	link := linkDir(t, dir, "guard.luau", target)
	before := treeNames(t, outside)
	zp := publisherPack(t, map[string]string{"guard.luau": "pwned"})
	_, err := extractSapiPack(zp, dir, false)
	requireUnchangedTree(t, outside, before)
	requireSameSymlink(t, link, target)
	if err == nil {
		t.Error("extract created the target of a dangling symlink")
	}
}

func TestExtractSapiPackDoesNotRewriteMatchingFinalSymlink(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	dest := filepath.Join(outside, "guard.luau")
	body := []byte("return 1")
	if err := os.WriteFile(dest, body, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Unix(1, 0)
	if err := os.Chtimes(dest, old, old); err != nil {
		t.Fatal(err)
	}
	link := linkDir(t, dir, "guard.luau", dest)
	zp := publisherPack(t, map[string]string{"guard.luau": string(body)})
	_, err := extractSapiPack(zp, dir, false)
	got, rerr := os.ReadFile(dest)
	if rerr != nil || string(got) != string(body) {
		t.Fatalf("target content = %q %v", got, rerr)
	}
	st, serr := os.Stat(dest)
	if serr != nil || !st.ModTime().Equal(old) {
		t.Errorf("rewrote the final symlink target (mtime %v)", st.ModTime())
	}
	requireSameSymlink(t, link, dest)
	if err == nil {
		t.Error("extract wrote through a final-path symlink whose contents matched")
	}
}

func TestSapiScaffoldDoesNotFollowDirectorySymlink(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	link := linkDir(t, dir, "lib", outside)
	before := treeNames(t, outside)
	_, _, err := sapiScaffold(dir, nil, false)
	requireUnchangedTree(t, outside, before)
	requireSameSymlink(t, link, outside)
	if err == nil {
		t.Error("scaffold wrote lib scripts through a directory symlink")
	}
}

func TestSapiScaffoldForceDoesNotOverwriteThroughDirectorySymlink(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	link := linkDir(t, dir, "lib", outside)
	dest := filepath.Join(outside, "guard.luau")
	if err := os.WriteFile(dest, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := sapiScaffold(dir, nil, true)
	got, rerr := os.ReadFile(dest)
	if rerr != nil || string(got) != "keep" {
		t.Fatalf("scaffold --force overwrote the directory symlink target: %q %v", got, rerr)
	}
	requireSameSymlink(t, link, outside)
	if err == nil {
		t.Error("scaffold --force wrote through a directory symlink")
	}
}

func TestSapiPullCommandDoesNotFollowDirectorySymlink(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	link := linkDir(t, dir, "lib", outside)
	blob := publisherPackBytes(t, map[string]string{"lib/guard.luau": "pwned"})
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tool/sapi/packs/active/download":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data":    map[string]any{"url": srv.URL + "/pack.zip", "checksum": "abc"},
			})
		case "/pack.zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(blob)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Setenv("BLAZIUM_ACCESS_TOKEN", "placeholder-access")
	t.Setenv("BLAZIUM_SECRET_KEY", "placeholder-secret")
	t.Setenv("BLAZIUM_API_URL", srv.URL)
	t.Setenv("BLAZIUM_UPLOAD_URL", srv.URL)
	viper.Reset()
	t.Cleanup(func() {
		viper.Reset()
		pf := rootCmd.PersistentFlags()
		for _, name := range []string{"access", "secret", "url", "upload"} {
			_ = viper.BindPFlag(name, pf.Lookup(name))
		}
	})
	initConfig()

	oldDir, oldForce, oldChannel := sapiPullDir, sapiPullForce, sapiPullChannel
	t.Cleanup(func() {
		sapiPullDir, sapiPullForce, sapiPullChannel = oldDir, oldForce, oldChannel
	})
	sapiPullDir, sapiPullForce, sapiPullChannel = dir, false, ""

	before := treeNames(t, outside)
	err := sapiPullCmd.RunE(sapiPullCmd, nil)
	requireUnchangedTree(t, outside, before)
	requireSameSymlink(t, link, outside)
	if err == nil {
		t.Error("sapi pull wrote through a directory symlink")
	}
}

func TestExtractDoesNotFollowCheckedOutDirectorySymlink(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init")
	git("config", "user.email", "dev@example.com")
	git("config", "user.name", "Dev")
	if err := os.Symlink(outside, filepath.Join(repo, "lib")); err != nil {
		t.Fatal(err)
	}
	git("add", "lib")
	git("commit", "-m", "link lib outside the tree")

	work := t.TempDir()
	cmd := exec.Command("git", "clone", repo, work)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	link := filepath.Join(work, "lib")
	got, err := os.Readlink(link)
	if err != nil || got != outside {
		t.Fatalf("checkout symlink = %q %v", got, err)
	}
	before := treeNames(t, outside)
	zp := publisherPack(t, map[string]string{"lib/guard.luau": "pwned"})
	_, err = extractSapiPack(zp, work, false)
	requireUnchangedTree(t, outside, before)
	requireSameSymlink(t, link, outside)
	if err == nil {
		t.Error("extract followed the directory symlink git checked out")
	}
}

func publisherPackBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	path := publisherPack(t, files)
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

// TestSapiPullSymlinkSwap fails if the creating open looks the path up again
// after a check. A real directory is replaced with a symlink to a directory
// outside the selection in the supervisor for that open, before the kernel
// walks the path.
func TestSapiPullSymlinkSwap(t *testing.T) {
	switch os.Getenv("SAPI_PULL_SYMLINK_CHILD") {
	case "":
		runSwapChild(t, "extract")
		runSwapChild(t, "scaffold")
	case "extract":
		runExtractSwap(t)
	case "scaffold":
		runScaffoldSwap(t)
	default:
		t.Fatalf("unknown child %q", os.Getenv("SAPI_PULL_SYMLINK_CHILD"))
	}
}

func runSwapChild(t *testing.T, mode string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSapiPullSymlinkSwap$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), "SAPI_PULL_SYMLINK_CHILD="+mode)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s swap child: %v\n%s", mode, err, out)
	}
}

func runExtractSwap(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib")
	if err := os.Mkdir(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	zp := publisherPack(t, map[string]string{"lib/guard.luau": "pwned"})
	before := treeNames(t, outside)
	err := withCreatSwap(t, lib, outside, func() error {
		_, err := extractSapiPack(zp, dir, false)
		return err
	})
	requireUnchangedTree(t, outside, before)
	if err == nil {
		t.Error("extract wrote lib/guard.luau after the directory was replaced with a symlink")
	}
}

func runScaffoldSwap(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib")
	if err := os.Mkdir(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	before := treeNames(t, outside)
	err := withCreatSwap(t, lib, outside, func() error {
		_, _, err := sapiScaffold(dir, nil, false)
		return err
	})
	requireUnchangedTree(t, outside, before)
	if err == nil {
		t.Error("scaffold wrote lib scripts after the directory was replaced with a symlink")
	}
}

type seccompData struct {
	Nr                 int32
	Arch               uint32
	InstructionPointer uint64
	Args               [6]uint64
}

type seccompNotif struct {
	ID    uint64
	Pid   uint32
	Flags uint32
	Data  seccompData
}

type seccompNotifResp struct {
	ID    uint64
	Val   int64
	Error int32
	Flags uint32
}

func withCreatSwap(t *testing.T, lib, outside string, fn func() error) error {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	fd, err := installCreatNotify()
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)

	swapped := make(chan error, 1)
	go superviseCreat(fd, lib, outside, swapped)

	err = fn()
	select {
	case swapErr := <-swapped:
		if swapErr != nil {
			t.Fatal(swapErr)
		}
	default:
		t.Fatal("the creating open was not observed")
	}
	return err
}

func installCreatNotify() (int, error) {
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return 0, err
	}
	// seccomp_data.arch is at offset 4, nr at 0, args[2] at 32.
	// OPENAT with O_CREAT and every OPENAT2 are delivered to the listener.
	// Other calls, including the listener thread, are allowed.
	ld := uint16(unix.BPF_LD | unix.BPF_W | unix.BPF_ABS)
	jeq := uint16(unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K)
	jset := uint16(unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K)
	ret := uint16(unix.BPF_RET | unix.BPF_K)
	filter := []unix.SockFilter{
		{Code: ld, K: 4},
		{Code: jeq, Jt: 1, Jf: 0, K: unix.AUDIT_ARCH_X86_64},
		{Code: ret, K: unix.SECCOMP_RET_KILL_PROCESS},
		{Code: ld, K: 0},
		{Code: jeq, Jt: 4, Jf: 0, K: unix.SYS_OPENAT},
		{Code: jeq, Jt: 5, Jf: 0, K: unix.SYS_OPENAT2},
		{Code: ret, K: unix.SECCOMP_RET_ALLOW},
		{Code: ret, K: unix.SECCOMP_RET_ALLOW},
		{Code: ret, K: unix.SECCOMP_RET_ALLOW},
		{Code: ld, K: 32},
		{Code: jset, Jt: 0, Jf: 1, K: unix.O_CREAT},
		{Code: ret, K: unix.SECCOMP_RET_USER_NOTIF},
		{Code: ret, K: unix.SECCOMP_RET_ALLOW},
	}
	prog := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	raw, _, errno := unix.Syscall(
		unix.SYS_SECCOMP,
		uintptr(unix.SECCOMP_SET_MODE_FILTER),
		uintptr(unix.SECCOMP_FILTER_FLAG_NEW_LISTENER),
		uintptr(unsafe.Pointer(&prog)),
	)
	if errno != 0 {
		return 0, errno
	}
	return int(raw), nil
}

func superviseCreat(fd int, lib, outside string, swapped chan<- error) {
	did := false
	for {
		var n seccompNotif
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(unix.SECCOMP_IOCTL_NOTIF_RECV), uintptr(unsafe.Pointer(&n)))
		if errno != 0 {
			if !did {
				swapped <- errno
			}
			return
		}
		path := cstring(n.Data.Args[1])
		var swapErr error
		if !did && creatingOpen(&n) && packFilePath(path) {
			did = true
			swapErr = swapDir(lib, outside)
			swapped <- swapErr
		}
		resp := seccompNotifResp{ID: n.ID, Flags: unix.SECCOMP_USER_NOTIF_FLAG_CONTINUE}
		unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(unix.SECCOMP_IOCTL_NOTIF_SEND), uintptr(unsafe.Pointer(&resp)))
		if did && swapErr != nil {
			return
		}
	}
}

func creatingOpen(n *seccompNotif) bool {
	switch n.Data.Nr {
	case int32(unix.SYS_OPENAT):
		return n.Data.Args[2]&uint64(unix.O_CREAT) != 0
	case int32(unix.SYS_OPENAT2):
		if n.Data.Args[2] == 0 || n.Data.Args[3] < 8 {
			return false
		}
		flags := *(*uint64)(unsafe.Pointer(uintptr(n.Data.Args[2])))
		return flags&uint64(unix.O_CREAT) != 0
	default:
		return false
	}
}

func packFilePath(path string) bool {
	base := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		base = path[i+1:]
		parent := path[:i]
		if parent != "lib" && !strings.HasSuffix(parent, "/lib") {
			return false
		}
	}
	return strings.HasSuffix(base, ".luau") || strings.HasSuffix(base, ".lua")
}

func swapDir(lib, outside string) error {
	if err := unix.Rename(lib, lib+".real"); err != nil {
		return err
	}
	return unix.Symlink(outside, lib)
}

func cstring(p uint64) string {
	if p == 0 {
		return ""
	}
	ptr := uintptr(p)
	var buf []byte
	for i := 0; i < 4096; i++ {
		c := *(*byte)(unsafe.Pointer(ptr + uintptr(i)))
		if c == 0 {
			return string(buf)
		}
		buf = append(buf, c)
	}
	return ""
}
