package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ProcessFiles handles the addfiles spec workflow
func ProcessFiles(client *Client, config *ParsedConfig) error {
	if config.FilesAsset == nil {
		return fmt.Errorf("files asset is nil")
	}

	asset := config.FilesAsset

	// Extract file paths from the files array
	var filePaths []string
	for _, fileEntry := range asset.Files {
		filePaths = append(filePaths, fileEntry.File)
	}

	// Validate all files exist
	fmt.Println("Validating files...")
	if err := ValidateFiles(filePaths); err != nil {
		return fmt.Errorf("file validation failed: %w", err)
	}
	fmt.Printf("All %d files validated\n", len(filePaths))

	// Create temporary zip file
	tempZip := filepath.Join(os.TempDir(), fmt.Sprintf("upload_%s.zip", uuid.New().String()))
	defer os.Remove(tempZip) // Clean up temp file

	fmt.Println("Creating zip file...")
	if err := CreateZip(tempZip, filePaths); err != nil {
		return fmt.Errorf("failed to create zip: %w", err)
	}
	fmt.Printf("Zip file created: %s\n", tempZip)

	// Calculate checksum
	fmt.Println("Calculating checksum...")
	checksum, err := CalculateSHA256(tempZip)
	if err != nil {
		return fmt.Errorf("failed to calculate checksum: %w", err)
	}
	fmt.Printf("Checksum: %s\n", checksum)

	// Get file size
	fileInfo, err := os.Stat(tempZip)
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}
	fileSize := fileInfo.Size()

	// Prepare form data
	formData := map[string]string{
		"build_uid": "", // We'll use build+type+version instead
		"build":     asset.Type,
		"type":      asset.Type,
		"version":   asset.Version,
		"channel":   asset.Channel,
		"os":        asset.OS,
		"arch":      asset.Arch,
		"checksum":  checksum,
	}

	// Generate session ID
	sessionID := uuid.New().String()

	// Upload file
	fmt.Println("Uploading file...")
	files := map[string]string{
		"file": tempZip,
	}

	headers := map[string]string{
		"X-Upload-Session-ID": sessionID,
	}

	_, err = client.PostMultipart("/tool/upload/files", formData, files, headers)
	if err != nil {
		// If upload fails, try resume
		fmt.Printf("Initial upload failed, attempting resume: %v\n", err)
		if resumeErr := uploadFileWithResume(client, tempZip, formData, fileSize, sessionID); resumeErr != nil {
			return fmt.Errorf("upload failed: %w", resumeErr)
		}
	}

	fmt.Println("File uploaded successfully")
	return nil
}

// uploadFileWithResume handles file upload with resume capability
func uploadFileWithResume(client *Client, filePath string, formData map[string]string, totalSize int64, sessionID string) error {
	const maxRetries = 3
	var uploadedBytes int64
	var lastError error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			fmt.Printf("  Retry attempt %d/%d...\n", attempt, maxRetries)
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}

		// Try uploading from current position
		resp, err := client.PostMultipartResume(
			"/tool/upload/files",
			formData,
			filePath,
			uploadedBytes,
			totalSize,
			sessionID,
		)

		if err != nil {
			lastError = err
			// Check if it's a retryable error
			if isRetryableError(err) && attempt < maxRetries-1 {
				continue
			}
			return fmt.Errorf("upload failed: %w", err)
		}

		// Check if upload is complete
		if resp.Data != nil {
			if partial, ok := resp.Data["partial"].(bool); ok && partial {
				// Partial upload successful - server should tell us the new position
				// For now, assume we uploaded the entire remaining file
				uploadedBytes = totalSize
				fmt.Printf("  Partial upload successful, continuing...\n")
				continue
			}
		}

		// Upload complete
		fmt.Printf("  Upload complete!\n")
		return nil
	}

	return fmt.Errorf("upload failed after %d attempts: %w", maxRetries, lastError)
}

// isRetryableError checks if an error is retryable
func isRetryableError(err error) bool {
	errStr := strings.ToLower(err.Error())
	// Check for network-related errors
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection") ||
		strings.Contains(errStr, "network") ||
		strings.Contains(errStr, "eof") ||
		strings.Contains(errStr, "broken pipe")
}
