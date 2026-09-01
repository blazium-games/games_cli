package main

import (
	"fmt"
	"os"
)

// ProcessBuild handles the build spec workflow
func ProcessBuild(client *Client, config *ParsedConfig) error {
	if config.BuildAsset == nil {
		return fmt.Errorf("build asset is nil")
	}

	asset := config.BuildAsset
	platforms := buildPlatforms(asset)
	var imageBuildID string

	for _, p := range platforms {
		display := asset.Title
		if display == "" {
			display = asset.Version
		}
		buildReq := map[string]interface{}{
			"build":       display,
			"build_type":  asset.Type,
			"version":     asset.Version,
			"title":       asset.Title,
			"description": asset.Description,
		}
		if p.OS != "" {
			buildReq["os"] = p.OS
			buildReq["arch"] = p.Arch
			buildReq["channel"] = p.Channel
		}
		if asset.Video != "" {
			buildReq["demo_url"] = asset.Video
		}
		if len(asset.Changelog) > 0 {
			var changelogItems []map[string]string
			for _, item := range asset.Changelog {
				changelogItems = append(changelogItems, map[string]string{
					"title":       item.Title,
					"description": item.Description,
				})
			}
			buildReq["changelog_items"] = changelogItems
		}

		fmt.Println("Uploading build information...")
		resp, err := client.PostJSON("/tool/upload/build", buildReq)
		if err != nil {
			return fmt.Errorf("failed to upload build: %w", err)
		}
		buildUID := responseBuildID(resp.Data)
		if buildUID == "" {
			return fmt.Errorf("build_id not found in response")
		}
		printBuildIDs(resp.Data, p)
		if imageBuildID == "" {
			imageBuildID = buildUID
		}
	}

	if len(asset.Images) > 0 {
		fmt.Println("Uploading images...")
		if err := uploadImages(client, imageBuildID, asset.Images); err != nil {
			return fmt.Errorf("failed to upload images: %w", err)
		}
		fmt.Printf("Successfully uploaded %d image(s)\n", len(asset.Images))
	}

	return nil
}

// uploadImages uploads images to the build
func uploadImages(client *Client, buildUID string, imagePaths []string) error {
	// Validate all image files exist
	for _, imgPath := range imagePaths {
		if _, err := os.Stat(imgPath); os.IsNotExist(err) {
			return fmt.Errorf("image file does not exist: %s", imgPath)
		}
	}

	// Upload all images in a single request (server expects multiple "image" fields)
	formData := map[string]string{
		"build_id": buildUID,
	}

	// Upload all images at once using the same field name
	_, err := client.PostMultipartMultipleFiles("/tool/upload/images", formData, "image", imagePaths, nil)
	if err != nil {
		return fmt.Errorf("failed to upload images: %w", err)
	}

	fmt.Printf("  Successfully uploaded %d image(s)\n", len(imagePaths))
	return nil
}
