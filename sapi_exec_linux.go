//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// sealSapiImage copies body into a memfd, seals it against any further write,
// resize or seal change, and checks the sealed contents against pin.
func sealSapiImage(body []byte, pin string) (*os.File, error) {
	fd, err := unix.MemfdCreate("games_sapi", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "games_sapi")
	fail := func(err error) (*os.File, error) {
		f.Close()
		return nil, err
	}
	if _, err := f.Write(body); err != nil {
		return fail(err)
	}
	seals := unix.F_SEAL_SEAL | unix.F_SEAL_SHRINK | unix.F_SEAL_GROW | unix.F_SEAL_WRITE
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, seals); err != nil {
		return fail(err)
	}
	info, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(f, 0, info.Size())); err != nil {
		return fail(err)
	}
	if hex.EncodeToString(h.Sum(nil)) != pin {
		return fail(fmt.Errorf("it does not match the games_sapi hash pinned in this chauffeur release"))
	}
	return f, nil
}

func sapiImagePath(f *os.File) string {
	return "/proc/self/fd/" + strconv.Itoa(int(f.Fd()))
}
