package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ValidateFiles checks if all files in the list exist
func ValidateFiles(filePaths []string) error {
	for _, filePath := range filePaths {
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			return fmt.Errorf("file does not exist: %s", filePath)
		}
	}
	return nil
}

// zipEntryNames names each file by its path relative to the deepest directory
// shared by all of them. Names that would collide (also ignoring case, which the
// uploader rejects) are an error.
func zipEntryNames(filePaths []string) ([]string, error) {
	abs := make([]string, len(filePaths))
	for i, p := range filePaths {
		a, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve %s: %w", p, err)
		}
		abs[i] = filepath.Clean(a)
	}
	base := ""
	for i, a := range abs {
		dir := filepath.Dir(a)
		if i == 0 {
			base = dir
			continue
		}
		for base != "" && !withinDir(dir, base) {
			parent := filepath.Dir(base)
			if parent == base {
				base = ""
				break
			}
			base = parent
		}
	}
	names := make([]string, len(abs))
	seen := map[string]string{}
	for i, a := range abs {
		name := a
		if base != "" {
			rel, err := filepath.Rel(base, a)
			if err != nil {
				return nil, fmt.Errorf("failed to name %s in the zip: %w", filePaths[i], err)
			}
			name = rel
		} else {
			name = strings.TrimPrefix(name, filepath.VolumeName(name))
		}
		name = strings.TrimLeft(filepath.ToSlash(name), "/")
		if name == "" || name == ".." || strings.HasPrefix(name, "../") {
			return nil, fmt.Errorf("cannot name %s in the zip", filePaths[i])
		}
		key := strings.ToLower(name)
		if prev, ok := seen[key]; ok {
			return nil, fmt.Errorf("%s and %s would have the same name in the zip (%s)", prev, filePaths[i], name)
		}
		seen[key] = filePaths[i]
		names[i] = name
	}
	return names, nil
}

func withinDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// CreateZip creates a zip file containing all the specified files.
// Symlinks are refused so a link cannot pack a file from outside the tree.
func CreateZip(outputPath string, filePaths []string) error {
	return createZip(outputPath, filePaths, false)
}

func createZip(outputPath string, filePaths []string, followSymlinks bool) error {
	names, err := zipEntryNames(filePaths)
	if err != nil {
		return err
	}
	zipFile, err := openZipOutput(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create zip file: %w", err)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	for i, filePath := range filePaths {
		info, err := os.Lstat(filePath)
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", filePath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if !followSymlinks {
				return fmt.Errorf("refusing symlink %s", filePath)
			}
			info, err = os.Stat(filePath)
			if err != nil {
				return fmt.Errorf("failed to open file %s: %w", filePath, err)
			}
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", filePath)
		}
		srcFile, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", filePath, err)
		}

		// Create a zip file header
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			srcFile.Close()
			return fmt.Errorf("failed to create zip header for %s: %w", filePath, err)
		}

		header.Name = names[i]
		header.Method = zip.Deflate

		// Create the file in the zip
		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			srcFile.Close()
			return fmt.Errorf("failed to create file in zip for %s: %w", filePath, err)
		}

		// Copy file contents to zip
		_, err = io.Copy(writer, srcFile)
		srcFile.Close()
		if err != nil {
			return fmt.Errorf("failed to copy file %s to zip: %w", filePath, err)
		}
	}

	return nil
}

// CalculateSHA256 calculates the SHA256 checksum of a file
func CalculateSHA256(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("failed to calculate checksum: %w", err)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// ScanImageDirectory scans a directory for image files and returns their paths
// Supported formats: png, jpg, jpeg, gif, webp, bmp, svg
func ScanImageDirectory(dirPath string) ([]string, error) {
	// Validate directory exists
	info, err := os.Stat(dirPath)
	if err != nil {
		return nil, fmt.Errorf("directory does not exist: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path is not a directory: %s", dirPath)
	}

	var imageFiles []string
	imageExtensions := map[string]bool{
		".png":  true,
		".jpg":  true,
		".jpeg": true,
		".gif":  true,
		".webp": true,
		".bmp":  true,
		".svg":  true,
	}

	err = filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() {
			ext := strings.ToLower(filepath.Ext(path))
			if imageExtensions[ext] {
				imageFiles = append(imageFiles, path)
			}
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error scanning directory: %w", err)
	}

	return imageFiles, nil
}

// fileScanOptions controls which files setfiles records.
// Dotfiles and symlinks are excluded unless the matching flag is set.
type fileScanOptions struct {
	IncludeHidden  bool
	FollowSymlinks bool
}

// secretFile reports names that commonly hold credentials.
func secretFile(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case ".env", ".npmrc", ".netrc", ".pypirc", ".git-credentials",
		"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519",
		"credentials", "credentials.json", "secrets.json", "secrets.yml", "secrets.yaml":
		return true
	}
	switch filepath.Ext(base) {
	case ".pem", ".key", ".p12", ".pfx":
		return true
	}
	return false
}

func openZipOutput(path string) (*os.File, error) {
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
			return nil, fmt.Errorf("refusing to write the zip through %s", path)
		}
		return os.OpenFile(path, os.O_RDWR|os.O_TRUNC, 0o600)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
}

// ScanFileDirectory collects regular files under dirPath.
// Dotfiles and dot-directories are skipped unless IncludeHidden is set.
// Symlinks are an error unless FollowSymlinks is set, and even then only a
// symlink to a regular file is recorded.
func ScanFileDirectory(dirPath string, opt fileScanOptions) ([]string, error) {
	info, err := os.Stat(dirPath)
	if err != nil {
		return nil, fmt.Errorf("directory does not exist: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path is not a directory: %s", dirPath)
	}

	var files []string

	err = filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path != dirPath && !opt.IncludeHidden && strings.HasPrefix(info.Name(), ".") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if !opt.FollowSymlinks {
				return fmt.Errorf("refusing symlink %s", path)
			}
			target, err := os.Stat(path)
			if err != nil {
				return fmt.Errorf("symlink %s: %w", path, err)
			}
			if !target.Mode().IsRegular() {
				return fmt.Errorf("refusing symlink %s that does not point at a file", path)
			}
			files = append(files, path)
			return nil
		}
		if info.IsDir() || !info.Mode().IsRegular() {
			return nil
		}
		files = append(files, path)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error scanning directory: %w", err)
	}

	return files, nil
}
