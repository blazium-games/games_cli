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

// CreateZip creates a zip file containing all the specified files
func CreateZip(outputPath string, filePaths []string) error {
	zipFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create zip file: %w", err)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	for _, filePath := range filePaths {
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

		// Use the base name of the file in the zip
		header.Name = filepath.Base(filePath)
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
