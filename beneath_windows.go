//go:build windows

package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// On Windows the directory is an os.Root, so no path below it can resolve
// outside it. Reparse points (symlinks and junctions) below it are refused, and
// each directory's identity is checked again once the file is open.
type beneathDir struct {
	root *os.Root
	name string
}

func openBeneathDir(dir string) (*beneathDir, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &beneathDir{root: root, name: dir}, nil
}

func (d *beneathDir) Close() error {
	return d.root.Close()
}

type beneathPath struct {
	rel  string
	dirs []string
	ids  []os.FileInfo
	name string
}

func isReparsePoint(fi os.FileInfo) bool {
	if fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return true
	}
	attrs, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	return !ok || attrs.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func (d *beneathDir) walk(rel string, create bool) (*beneathPath, error) {
	dirs, base, err := splitBeneath(rel)
	if err != nil {
		return nil, err
	}
	p := &beneathPath{rel: rel}
	prefix := ""
	for _, name := range dirs {
		prefix = filepath.Join(prefix, name)
		fi, err := d.root.Lstat(prefix)
		if create && errors.Is(err, fs.ErrNotExist) {
			if err := d.root.Mkdir(prefix, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
				return nil, err
			}
			fi, err = d.root.Lstat(prefix)
		}
		if err != nil {
			return nil, err
		}
		if isReparsePoint(fi) {
			return nil, errBeneathSymlink(d.name, rel, filepath.ToSlash(prefix))
		}
		if !fi.IsDir() {
			return nil, fmt.Errorf("refusing to write %s in %s: %s is not a directory", rel, d.name, filepath.ToSlash(prefix))
		}
		p.dirs = append(p.dirs, prefix)
		p.ids = append(p.ids, fi)
	}
	p.name = filepath.Join(prefix, base)
	return p, nil
}

// verify refuses when a directory on the path, or the file itself, is no longer
// the one that was checked under that name.
func (d *beneathDir) verify(p *beneathPath, f *os.File) error {
	for i, dir := range p.dirs {
		fi, err := d.root.Lstat(dir)
		if err != nil || isReparsePoint(fi) || !os.SameFile(fi, p.ids[i]) {
			return errBeneathChanged(d.name, p.rel, filepath.ToSlash(dir))
		}
	}
	got, err := f.Stat()
	if err != nil {
		return err
	}
	fi, err := d.root.Lstat(p.name)
	if err != nil || isReparsePoint(fi) || !fi.Mode().IsRegular() || !os.SameFile(fi, got) {
		return errBeneathChanged(d.name, p.rel, p.rel)
	}
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &info); err != nil {
		return err
	}
	if info.NumberOfLinks != 1 {
		return fmt.Errorf("refusing to use %s: it has more than one link", f.Name())
	}
	return nil
}

// checkFinal refuses a reparse point or a non-regular file at p.name.
func (d *beneathDir) checkFinal(p *beneathPath) (os.FileInfo, error) {
	fi, err := d.root.Lstat(p.name)
	if err != nil {
		return nil, err
	}
	if isReparsePoint(fi) {
		return nil, errBeneathSymlink(d.name, p.rel, p.rel)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("refusing to use %s in %s: not a regular file", p.rel, d.name)
	}
	return fi, nil
}

func (d *beneathDir) ReadFile(rel string) ([]byte, error) {
	p, err := d.walk(rel, false)
	if err != nil {
		return nil, err
	}
	if _, err := d.checkFinal(p); err != nil {
		return nil, err
	}
	f, err := d.root.Open(p.name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := d.verify(p, f); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}

func (d *beneathDir) Lexists(rel string) (bool, error) {
	p, err := d.walk(rel, false)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := d.root.Lstat(p.name); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (d *beneathDir) WriteFile(rel string, body []byte, perm os.FileMode) error {
	p, err := d.walk(rel, true)
	if err != nil {
		return err
	}
	if _, err := d.checkFinal(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	created := true
	f, err := d.root.OpenFile(p.name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if errors.Is(err, fs.ErrExist) {
		created = false
		f, err = d.root.OpenFile(p.name, os.O_WRONLY, 0)
	}
	if err != nil {
		return err
	}
	fail := func(err error) error {
		st, serr := f.Stat()
		f.Close()
		if created && serr == nil {
			if fi, lerr := d.root.Lstat(p.name); lerr == nil && os.SameFile(fi, st) {
				d.root.Remove(p.name)
			}
		}
		return err
	}
	if err := d.verify(p, f); err != nil {
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
