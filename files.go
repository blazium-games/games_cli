package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ProcessFiles handles the addfiles spec workflow: create a platform build,
// zip and upload its files, then upload symbols if the spec names them.
func ProcessFiles(client *Client, config *ParsedConfig) error {
	if config.FilesAsset == nil {
		return usageErrorf("files asset is missing")
	}
	asset := config.FilesAsset
	if err := validateFilesAsset(asset); err != nil {
		return err
	}
	platform := PlatformSpec{OS: asset.OS, Arch: asset.Arch, Channel: asset.Channel}

	filePaths := make([]string, 0, len(asset.Files))
	for _, fileEntry := range asset.Files {
		filePaths = append(filePaths, fileEntry.File)
	}
	logln("Validating files...")
	if err := ValidateFiles(filePaths); err != nil {
		return usageErrorf("%v", err)
	}
	logf("All %d files validated\n", len(filePaths))
	if asset.Symbols != "" {
		up, err := prepareSymbols(asset.Symbols)
		if err != nil {
			return err
		}
		up.cleanup()
	}

	tempZip := filepath.Join(os.TempDir(), fmt.Sprintf("chauffeur_upload_%d.zip", time.Now().UnixNano()))
	defer os.Remove(tempZip)
	logln("Creating zip file...")
	if err := CreateZip(tempZip, filePaths); err != nil {
		return usageErrorf("failed to create zip: %v", err)
	}
	st, err := os.Stat(tempZip)
	if err != nil {
		return err
	}
	if st.Size() > maxBuildFileBytes {
		return usageErrorf("the zipped files are %s; a build file can be at most 5 GB", sizeText(st.Size()))
	}
	logln("Calculating checksum...")
	checksum, err := CalculateSHA256(tempZip)
	if err != nil {
		return err
	}
	logf("Checksum: %s (%s)\n", checksum, sizeText(st.Size()))

	title := asset.Version + " " + platform.OS + "/" + platform.Arch
	buildReq := map[string]any{
		"build":       title,
		"build_type":  asset.Type,
		"version":     asset.Version,
		"os":          platform.OS,
		"arch":        platform.Arch,
		"channel":     platform.Channel,
		"title":       title,
		"description": title,
	}
	if asset.EngineVersion != "" {
		buildReq["engine_version"] = asset.EngineVersion
	}
	logln("Creating platform build...")
	buildResp, err := client.PostJSON("/tool/upload/build", buildReq)
	if err != nil {
		return err
	}
	buildID := responseBuildID(buildResp.Data)
	if buildID == "" {
		return &apiError{Status: 200, Message: "the service did not return a build_id"}
	}
	printBuildIDs(buildResp.Data, platform)
	appID, _ := buildResp.Data["app_id"].(string)

	logln("Uploading file...")
	data, err := uploadBuildFile(client, buildFileUpload{
		Path: tempZip, Size: st.Size(), Checksum: checksum, BuildID: buildID, Platform: platform,
	})
	if err != nil {
		return err
	}
	fileUID, _ := data["file_uid"].(string)
	status, _ := data["status"].(string)
	logf("File uploaded. File UID: %s, status: %s\n", fileUID, status)

	result := buildResult{BuildID: buildID, AppID: appID, OS: platform.OS, Arch: platform.Arch,
		Channel: platform.Channel, FileUID: fileUID, Checksum: checksum}
	if asset.Symbols != "" {
		sym, err := uploadSymbols(client, buildID, asset.Symbols)
		if err != nil {
			return err
		}
		result.Symbols = int64Of(sym["count"])
		logf("Uploaded %d symbol file(s).\n", result.Symbols)
	}

	logln("Upload completed successfully")
	if jsonOutput {
		return emit(map[string]any{"ok": true, "builds": []buildResult{result}})
	}
	return nil
}
