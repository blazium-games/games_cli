//go:build unix

package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type beneathDir struct {
	fd   int
	name string
}

// openBeneathDir creates dir if it is missing and opens it. dir itself may be
// a symlink: it is the directory the developer chose.
func openBeneathDir(dir string) (*beneathDir, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: dir, Err: err}
	}
	return &beneathDir{fd: fd, name: dir}, nil
}

func (d *beneathDir) Close() error {
	return unix.Close(d.fd)
}

// beneathStep is a directory opened on the way to a file, with the name it was
// opened by in its parent and the identity it had then.
type beneathStep struct {
	parent int
	name   string
	fd     int
	dev    uint64
	ino    uint64
}

type beneathPath struct {
	rel    string
	steps  []beneathStep
	parent int
	base   string
}

func (p *beneathPath) close() {
	for _, s := range p.steps {
		unix.Close(s.fd)
	}
}

// walk opens each directory of rel below d without following a symlink,
// creating missing ones when create is set.
func (d *beneathDir) walk(rel string, create bool) (*beneathPath, error) {
	dirs, base, err := splitBeneath(rel)
	if err != nil {
		return nil, err
	}
	p := &beneathPath{rel: rel, parent: d.fd, base: base}
	for i, name := range dirs {
		shown := path.Join(dirs[:i+1]...)
		if create {
			if err := unix.Mkdirat(p.parent, name, 0o755); err != nil && err != unix.EEXIST {
				p.close()
				return nil, &os.PathError{Op: "mkdirat", Path: filepath.Join(d.name, shown), Err: err}
			}
		}
		fd, err := unix.Openat(p.parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			err = d.explain(p.parent, name, rel, shown, err)
			p.close()
			return nil, err
		}
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil {
			unix.Close(fd)
			p.close()
			return nil, &os.PathError{Op: "fstat", Path: filepath.Join(d.name, shown), Err: err}
		}
		p.steps = append(p.steps, beneathStep{parent: p.parent, name: name, fd: fd, dev: uint64(st.Dev), ino: uint64(st.Ino)})
		p.parent = fd
	}
	return p, nil
}

// explain turns a failed no-follow open of name in parent into an error that
// says when a symlink was refused.
func (d *beneathDir) explain(parent int, name, rel, shown string, err error) error {
	var st unix.Stat_t
	if unix.Fstatat(parent, name, &st, unix.AT_SYMLINK_NOFOLLOW) == nil && st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return errBeneathSymlink(d.name, rel, shown)
	}
	return &os.PathError{Op: "openat", Path: filepath.Join(d.name, filepath.FromSlash(shown)), Err: err}
}

func sameAt(parent int, name string, dev, ino uint64) bool {
	var st unix.Stat_t
	if unix.Fstatat(parent, name, &st, unix.AT_SYMLINK_NOFOLLOW) != nil {
		return false
	}
	return uint64(st.Dev) == dev && uint64(st.Ino) == ino
}

// verify refuses when a directory on the path, or the file itself, is no longer
// the one that was opened under that name.
func (d *beneathDir) verify(p *beneathPath, dev, ino uint64) error {
	for i, s := range p.steps {
		if !sameAt(s.parent, s.name, s.dev, s.ino) {
			shown := ""
			for _, t := range p.steps[:i+1] {
				shown = path.Join(shown, t.name)
			}
			return errBeneathChanged(d.name, p.rel, shown)
		}
	}
	if !sameAt(p.parent, p.base, dev, ino) {
		return errBeneathChanged(d.name, p.rel, p.rel)
	}
	return nil
}

func regularSingleLink(f *os.File) (dev, ino uint64, err error) {
	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		return 0, 0, &os.PathError{Op: "fstat", Path: f.Name(), Err: err}
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return 0, 0, fmt.Errorf("refusing to use %s: not a regular file", f.Name())
	}
	if uint64(st.Nlink) != 1 {
		return 0, 0, fmt.Errorf("refusing to use %s: it has more than one link", f.Name())
	}
	return uint64(st.Dev), uint64(st.Ino), nil
}

// ReadFile reads the regular file at rel. A missing file or directory gives an
// error matching fs.ErrNotExist; a symlink anywhere below d is refused.
func (d *beneathDir) ReadFile(rel string) ([]byte, error) {
	p, err := d.walk(rel, false)
	if err != nil {
		return nil, err
	}
	defer p.close()
	fd, err := unix.Openat(p.parent, p.base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, d.explain(p.parent, p.base, rel, rel, err)
	}
	f := os.NewFile(uintptr(fd), filepath.Join(d.name, filepath.FromSlash(rel)))
	defer f.Close()
	if _, _, err := regularSingleLink(f); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}

// Lexists reports whether anything, including a symlink, is at rel. A symlink
// among the directories leading to rel is refused.
func (d *beneathDir) Lexists(rel string) (bool, error) {
	p, err := d.walk(rel, false)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer p.close()
	var st unix.Stat_t
	switch err := unix.Fstatat(p.parent, p.base, &st, unix.AT_SYMLINK_NOFOLLOW); err {
	case nil:
		return true, nil
	case unix.ENOENT:
		return false, nil
	default:
		return false, &os.PathError{Op: "fstatat", Path: filepath.Join(d.name, filepath.FromSlash(rel)), Err: err}
	}
}

// WriteFile replaces the contents of rel with body, creating it and its
// directories if needed. It refuses a symlink anywhere below d, including at
// rel, and writes nothing if any directory on the way was replaced while the
// file was opened.
func (d *beneathDir) WriteFile(rel string, body []byte, perm os.FileMode) error {
	p, err := d.walk(rel, true)
	if err != nil {
		return err
	}
	defer p.close()
	const flags = unix.O_WRONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	created := true
	fd, err := unix.Openat(p.parent, p.base, flags|unix.O_CREAT|unix.O_EXCL, uint32(perm.Perm()))
	if err == unix.EEXIST {
		created = false
		fd, err = unix.Openat(p.parent, p.base, flags, 0)
	}
	if err != nil {
		return d.explain(p.parent, p.base, rel, rel, err)
	}
	f := os.NewFile(uintptr(fd), filepath.Join(d.name, filepath.FromSlash(rel)))
	var dev, ino uint64
	fail := func(err error) error {
		if created && sameAt(p.parent, p.base, dev, ino) {
			unix.Unlinkat(p.parent, p.base, 0)
		}
		f.Close()
		return err
	}
	if dev, ino, err = regularSingleLink(f); err != nil {
		return fail(err)
	}
	if err := d.verify(p, dev, ino); err != nil {
		return fail(err)
	}
	if err := f.Truncate(0); err != nil {
		return fail(err)
	}
	if _, err := f.Write(body); err != nil {
		return fail(err)
	}
	return f.Close()
}
