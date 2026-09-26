package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
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
	tempZip := filepath.Join(os.TempDir(), fmt.Sprintf("upload_%d.zip", time.Now().UnixNano()))
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

	platform := PlatformSpec{OS: asset.OS, Arch: asset.Arch, Channel: asset.Channel}
	if platform.Channel == "" {
		platform.Channel = "stable"
	}
	title := asset.Version + " " + asset.OS + "/" + asset.Arch
	fmt.Println("Creating platform build...")
	buildResp, err := client.PostJSON("/tool/upload/build", map[string]interface{}{
		"build":       title,
		"build_type":  asset.Type,
		"version":     asset.Version,
		"os":          asset.OS,
		"arch":        asset.Arch,
		"channel":     platform.Channel,
		"title":       title,
		"description": title,
	})
	if err != nil {
		return fmt.Errorf("failed to create platform build: %w", err)
	}
	buildID := responseBuildID(buildResp.Data)
	if buildID == "" {
		return fmt.Errorf("build_id not found in response")
	}
	printBuildIDs(buildResp.Data, platform)

	formData := map[string]string{
		"build_id":   buildID,
		"build":      title,
		"build_type": asset.Type,
		"version":    asset.Version,
		"channel":    platform.Channel,
		"os":         asset.OS,
		"arch":       asset.Arch,
		"checksum":   checksum,
	}

	fmt.Println("Uploading file...")
	files := map[string]string{
		"file": tempZip,
	}

	resp, sessionID, err := client.PostMultipart(client.filesURL("/tool/upload/files"), formData, files, nil)
	if err != nil {
		return fmt.Errorf("initial upload failed: %w", err)
	}

	if isUploadComplete(resp) {
		printFileUploadResult(resp.Data, platform)
		return nil
	}

	// Upload is incomplete, need to resume
	fmt.Println("Upload incomplete, resuming...")
	if sessionID == "" {
		// Try to get session_id from response data
		if sessionIDFromData, ok := resp.Data["session_id"].(string); ok {
			sessionID = sessionIDFromData
		} else {
			return fmt.Errorf("session_id not found in response")
		}
	}

	// Resume upload
	if err := uploadFileWithResume(client, tempZip, formData, fileSize, sessionID); err != nil {
		return fmt.Errorf("resume upload failed: %w", err)
	}

	fmt.Println("File uploaded successfully")
	return nil
}

// uploadFileWithResume handles file upload with resume capability
func uploadFileWithResume(client *Client, filePath string, formData map[string]string, totalSize int64, sessionID string) error {
	var uploadedBytes int64

	for {
		// Try uploading from current position
		resp, newSessionID, err := client.PostMultipartResume(
			client.filesURL("/tool/upload/files"),
			formData,
			filePath,
			uploadedBytes,
			totalSize,
			sessionID,
		)

		if err != nil {
			// Check if it's a retryable error
			if isRetryableError(err) {
				fmt.Printf("  Retryable error, retrying...\n")
				time.Sleep(2 * time.Second)
				continue
			}
			return fmt.Errorf("upload failed: %w", err)
		}

		// Update session ID if server provided a new one
		if newSessionID != "" {
			sessionID = newSessionID
		}

		// Check if upload is complete
		if isUploadComplete(resp) {
			printFileUploadResult(resp.Data, PlatformSpec{})
			return nil
		}

		// Upload is incomplete, extract current_size and continue
		if resp.Data != nil {
			// Try to get current_size from response
			var currentSize int64
			if currentSizeFloat, ok := resp.Data["current_size"].(float64); ok {
				currentSize = int64(currentSizeFloat)
			} else if currentSizeInt, ok := resp.Data["current_size"].(int64); ok {
				currentSize = currentSizeInt
			} else if currentSizeInt, ok := resp.Data["current_size"].(int); ok {
				currentSize = int64(currentSizeInt)
			} else {
				// If we can't get current_size, assume we uploaded everything
				currentSize = totalSize
			}

			// Get progress if available
			if progress, ok := resp.Data["progress"].(float64); ok {
				fmt.Printf("  Upload progress: %.1f%% (%d/%d bytes)\n", progress, currentSize, totalSize)
			} else {
				fmt.Printf("  Upload progress: %d/%d bytes\n", currentSize, totalSize)
			}

			// Check if we've reached the end
			if currentSize >= totalSize {
				fmt.Printf("  Upload complete!\n")
				return nil
			}

			uploadedBytes = currentSize

			// Update session ID from response data if available
			if sessionIDFromData, ok := resp.Data["session_id"].(string); ok && sessionIDFromData != "" {
				sessionID = sessionIDFromData
			}

			// Continue with next chunk
			continue
		}

		// If we can't determine status, assume complete
		fmt.Printf("  Upload complete!\n")
		return nil
	}
}

// isUploadComplete checks if the upload response indicates completion
func isUploadComplete(resp *APIResponse) bool {
	if resp.Data == nil {
		return false
	}
	// Complete uploads have file_uid
	_, hasFileUID := resp.Data["file_uid"]
	// Incomplete uploads have session_id
	_, hasSessionID := resp.Data["session_id"]
	return hasFileUID && !hasSessionID
}

// isRetryableError checks if an error is retryable
func isRetryableError(err error) bool {
	errStr := strings.ToLower(err.Error())
	// Check for network-related errors
	if strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection") ||
		strings.Contains(errStr, "network") ||
		strings.Contains(errStr, "eof") ||
		strings.Contains(errStr, "broken pipe") {
		return true
	}
	// Check for specific API error codes that are retryable
	// 5000 - Internal Server Error (may be transient)
	if strings.Contains(errStr, "[5000]") {
		return true
	}
	// 4045 - Upload Session Not Found (session may have expired, retry with new session)
	if strings.Contains(errStr, "[4045]") {
		return true
	}
	return false
}

func printFileUploadResult(data map[string]interface{}, p PlatformSpec) {
	fileUID, _ := data["file_uid"].(string)
	status, _ := data["status"].(string)
	fmt.Printf("File uploaded successfully. File UID: %s, Status: %s\n", fileUID, status)
	if responseBuildID(data) != "" {
		printBuildIDs(data, p)
	}
}
