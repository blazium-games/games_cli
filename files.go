package main

import (
	"fmt"
	"net/url"
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

	buildData, err := platformBuild(client, asset, platform)
	if err != nil {
		return err
	}
	buildID := responseBuildID(buildData)
	printBuildIDs(buildData, platform)
	appID, _ := buildData["app_id"].(string)

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

// platformBuild returns the build the files belong to. A build already
// registered for the same version, type and platform (for example by
// chauffeur build) is reused as is: registering it again would replace its
// title, description and changelog.
func platformBuild(client *Client, asset *FilesAsset, p PlatformSpec) (map[string]any, error) {
	if existing := findBuild(client, asset.Type, asset.Version, p); existing != nil {
		logf("Using the existing %s build for %s/%s (%s).\n", asset.Version, p.OS, p.Arch, p.Channel)
		if ev, _ := existing["engine_version"].(string); asset.EngineVersion != "" && ev != asset.EngineVersion {
			logf("Warning: engine_version %s differs from the registered build's %q; set it in build.yml and run chauffeur build to change it.\n", asset.EngineVersion, ev)
		}
		return existing, nil
	}
	title := asset.Version + " " + p.OS + "/" + p.Arch
	buildReq := map[string]any{
		"build":       title,
		"build_type":  asset.Type,
		"version":     asset.Version,
		"os":          p.OS,
		"arch":        p.Arch,
		"channel":     p.Channel,
		"title":       title,
		"description": title,
	}
	if asset.EngineVersion != "" {
		buildReq["engine_version"] = asset.EngineVersion
	}
	logln("Creating platform build...")
	resp, err := client.PostJSON("/tool/upload/build", buildReq)
	if err != nil {
		return nil, err
	}
	if responseBuildID(resp.Data) == "" {
		return nil, &apiError{Status: 200, Message: "the service did not return a build_id"}
	}
	return resp.Data, nil
}

// findBuild looks up a build with exactly this version, type and platform.
// Lookup failures fall back to registering the build.
func findBuild(client *Client, typ, version string, p PlatformSpec) map[string]any {
	q := url.Values{"version": {version}, "os": {p.OS}, "arch": {p.Arch}, "channel": {p.Channel}, "page_size": {"100"}}
	resp, err := client.GetJSON("/tool/builds?" + q.Encode())
	if err != nil {
		return nil
	}
	builds, _ := resp.Data["builds"].([]any)
	for _, raw := range builds {
		b, _ := raw.(map[string]any)
		if b == nil || responseBuildID(b) == "" {
			continue
		}
		if b["version"] == version && b["build_type"] == typ && b["os"] == p.OS && b["arch"] == p.Arch && b["channel"] == p.Channel {
			return b
		}
	}
	return nil
}
