package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
)

var logger *logrus.Logger

func init() {
	_ = godotenv.Load(".env")
	initLogger()
}

func initLogger() {
	logger = logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
	logger.SetOutput(os.Stdout)
	logger.SetLevel(logrus.InfoLevel)
}

func main() {
	filePath := flag.String("file", "", "Path to a single file to upload")
	folderPath := flag.String("folder", "", "Path to a folder containing files to upload")
	cdnPath := flag.String("path", "", "Destination path on CDN (required, e.g., 'images/', 'builds/')")
	acl := flag.String("acl", "public-read", "ACL setting (default: 'public-read')")
	storageType := flag.String("storage", "STANDARD", "Storage type (default: 'STANDARD')")
	flag.Parse()

	// Validate that either file or folder is provided (but not both)
	if *filePath == "" && *folderPath == "" {
		logger.Fatal("Error: either --file or --folder must be provided")
	}
	if *filePath != "" && *folderPath != "" {
		logger.Fatal("Error: --file and --folder cannot be used together")
	}

	// Validate that path is provided
	if *cdnPath == "" {
		logger.Fatal("Error: --path is required")
	}

	// Ensure cdnPath ends with a slash
	if !strings.HasSuffix(*cdnPath, "/") {
		*cdnPath = *cdnPath + "/"
	}

	// Validate environment variables
	if err := validateEnvVars(); err != nil {
		logger.WithError(err).Fatal("Error: missing required environment variables")
	}

	var err error
	if *filePath != "" {
		err = uploadSingleFile(*filePath, *cdnPath, *acl, *storageType)
	} else {
		err = uploadFolder(*folderPath, *cdnPath, *acl, *storageType)
	}

	if err != nil {
		logger.WithError(err).Fatal("Upload failed")
	}

	logger.Info("All uploads completed successfully")
}

func validateEnvVars() error {
	required := []string{"CDN_SPACE", "CDN_REGION", "CDN_KEY", "CDN_SECRET", "CDN_BASEURL", "SPACE_PATH"}
	var missing []string

	for _, envVar := range required {
		if os.Getenv(envVar) == "" {
			missing = append(missing, envVar)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	return nil
}

func uploadSingleFile(filePath, cdnPath, acl, storageType string) error {
	// Validate file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("file does not exist: %s", filePath)
	}

	// Get absolute path and filename
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}
	dir := filepath.Dir(absPath)
	filename := filepath.Base(absPath)

	logger.WithFields(logrus.Fields{
		"file":     filePath,
		"cdn_path": cdnPath,
	}).Info("Uploading file to CDN")

	// Build space path
	spacePath := os.Getenv("SPACE_PATH") + cdnPath

	// Upload file
	if err := putS3(dir, filename, spacePath, acl, storageType); err != nil {
		return fmt.Errorf("failed to upload file: %w", err)
	}

	// Display final CDN URL
	cdnBase := os.Getenv("CDN_BASEURL")
	cdnURL := fmt.Sprintf("%s%s%s", cdnBase, spacePath, filename)
	logger.WithFields(logrus.Fields{
		"file":    filename,
		"cdn_url": cdnURL,
	}).Info("File uploaded successfully")

	return nil
}

func uploadFolder(folderPath, cdnPath, acl, storageType string) error {
	// Validate folder exists
	info, err := os.Stat(folderPath)
	if os.IsNotExist(err) {
		return fmt.Errorf("folder does not exist: %s", folderPath)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", folderPath)
	}

	// Read directory contents (non-recursive, top-level only)
	entries, err := os.ReadDir(folderPath)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}

	// Filter out directories, keep only files
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() {
			files = append(files, entry.Name())
		}
	}

	if len(files) == 0 {
		logger.Warn("No files found in folder")
		return nil
	}

	logger.WithFields(logrus.Fields{
		"folder":   folderPath,
		"cdn_path": cdnPath,
		"count":    len(files),
	}).Info("Uploading files from folder to CDN")

	// Build space path
	spacePath := os.Getenv("SPACE_PATH") + cdnPath
	cdnBase := os.Getenv("CDN_BASEURL")

	// Upload each file
	successCount := 0
	for i, filename := range files {
		logger.WithFields(logrus.Fields{
			"file":  filename,
			"index": i + 1,
			"total": len(files),
		}).Info("Uploading file")

		if err := putS3(folderPath, filename, spacePath, acl, storageType); err != nil {
			logger.WithError(err).WithFields(logrus.Fields{
				"file": filename,
			}).Error("Failed to upload file")
			continue
		}

		cdnURL := fmt.Sprintf("%s%s%s", cdnBase, spacePath, filename)
		logger.WithFields(logrus.Fields{
			"file":    filename,
			"cdn_url": cdnURL,
		}).Info("File uploaded successfully")
		successCount++
	}

	logger.WithFields(logrus.Fields{
		"success": successCount,
		"total":   len(files),
	}).Info("Folder upload completed")

	if successCount < len(files) {
		return fmt.Errorf("uploaded %d of %d files", successCount, len(files))
	}

	return nil
}

func putS3(path, file, spacePath, acl, storageType string) error {
	// Load required ENV vars
	space := os.Getenv("CDN_SPACE")
	region := os.Getenv("CDN_REGION")
	key := os.Getenv("CDN_KEY")
	secret := os.Getenv("CDN_SECRET")

	// Validate required env vars
	if space == "" || region == "" || key == "" || secret == "" {
		return fmt.Errorf("missing one or more required environment variables: SPACE, REGION, KEY, SECRET")
	}

	// Validate required function inputs
	if acl == "" {
		return fmt.Errorf("missing required parameter: acl")
	}
	if storageType == "" {
		return fmt.Errorf("missing required parameter: storageType")
	}

	fullPath := filepath.Join(path, file)

	// Open file
	f, err := os.Open(fullPath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	// Read file contents
	fileContents, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// Detect MIME type
	contentType := mime.TypeByExtension(filepath.Ext(file))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	// Prepare headers
	date := time.Now().UTC().Format("Mon, 02 Jan 2006 15:04:05 -0700")
	aclHeader := fmt.Sprintf("x-amz-acl:%s", acl)
	storageHeader := fmt.Sprintf("x-amz-storage-class:%s", storageType)

	// Build string to sign
	stringToSign := fmt.Sprintf("PUT\n\n%s\n%s\n%s\n%s\n/%s%s%s", contentType, date, aclHeader, storageHeader, space, spacePath, file)

	// Generate signature
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(stringToSign))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	// Build request
	url := fmt.Sprintf("https://%s.%s.digitaloceanspaces.com%s%s", space, region, spacePath, file)
	req, err := http.NewRequest("PUT", url, bytes.NewReader(fileContents))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Host", fmt.Sprintf("%s.%s.digitaloceanspaces.com", space, region))
	req.Header.Set("Date", date)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("x-amz-storage-class", storageType)
	req.Header.Set("x-amz-acl", acl)
	req.Header.Set("Authorization", fmt.Sprintf("AWS %s:%s", key, signature))

	// Execute request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upload request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed: %s\nResponse: %s", resp.Status, body)
	}

	return nil
}
