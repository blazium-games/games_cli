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

// CreateZip creates a zip file containing all the specified files
func CreateZip(outputPath string, filePaths []string) error {
	names, err := zipEntryNames(filePaths)
	if err != nil {
		return err
	}
	zipFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create zip file: %w", err)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	for i, filePath := range filePaths {
		// Open the source file
		srcFile, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", filePath, err)
		}

		// Get file info for the header
		info, err := srcFile.Stat()
		if err != nil {
			srcFile.Close()
			return fmt.Errorf("failed to get file info for %s: %w", filePath, err)
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

// ScanFileDirectory scans a directory for all files (not just images) and returns their paths
// Recursively walks the directory and collects all file paths
func ScanFileDirectory(dirPath string) ([]string, error) {
	// Validate directory exists
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

		if !info.IsDir() {
			files = append(files, path)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error scanning directory: %w", err)
	}

	return files, nil
}
