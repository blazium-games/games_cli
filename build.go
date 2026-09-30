package main

// buildResult is one created build_id, reported with --json.
type buildResult struct {
	BuildID  string `json:"build_id"`
	AppID    string `json:"app_id,omitempty"`
	App      string `json:"app,omitempty"`
	OS       string `json:"os,omitempty"`
	Arch     string `json:"arch,omitempty"`
	Channel  string `json:"channel,omitempty"`
	Symbols  int64  `json:"symbols,omitempty"`
	FileUID  string `json:"file_uid,omitempty"`
	Checksum string `json:"checksum,omitempty"`
}

// ProcessBuild handles the build spec workflow
func ProcessBuild(client *Client, config *ParsedConfig) error {
	if config.BuildAsset == nil {
		return usageErrorf("build asset is missing")
	}
	asset := config.BuildAsset
	if err := validateBuildAsset(asset); err != nil {
		return err
	}
	platforms := buildPlatforms(asset)
	if asset.Symbols != "" && len(platforms) != 1 {
		return usageErrorf("symbols belong to one build: use a single platform (--os/--arch) with --symbols, or run chauffeur symbols per build_id")
	}
	if asset.Symbols != "" {
		up, err := prepareSymbols(asset.Symbols)
		if err != nil {
			return err
		}
		up.cleanup()
	}
	for _, img := range buildImages(asset.Media, asset.Images) {
		if err := checkImage(img.path, img.slot); err != nil {
			return err
		}
	}

	display := asset.Title
	if display == "" {
		display = asset.Version
	}
	var results []buildResult
	for _, p := range platforms {
		buildReq := map[string]any{
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
		if asset.EngineVersion != "" {
			buildReq["engine_version"] = asset.EngineVersion
		}
		asset.App.addTo(buildReq)
		if asset.Video != "" {
			buildReq["demo_url"] = asset.Video
		}
		if len(asset.Changelog) > 0 {
			items := make([]map[string]string, 0, len(asset.Changelog))
			for _, item := range asset.Changelog {
				items = append(items, map[string]string{"title": item.Title, "description": item.Description})
			}
			buildReq["changelog_items"] = items
		}

		logln("Uploading build information...")
		resp, err := client.PostJSON("/tool/upload/build", buildReq)
		if err != nil {
			return err
		}
		buildUID := responseBuildID(resp.Data)
		if buildUID == "" {
			return &apiError{Status: 200, Message: "the service did not return a build_id"}
		}
		printBuildIDs(resp.Data, p)
		appID, _ := resp.Data["app_id"].(string)
		results = append(results, buildResult{BuildID: buildUID, AppID: appID, App: asset.App.id(), OS: p.OS, Arch: p.Arch, Channel: p.Channel})
	}

	if asset.Symbols != "" {
		data, err := uploadSymbols(client, results[0].BuildID, asset.Symbols)
		if err != nil {
			return err
		}
		results[0].Symbols = int64Of(data["count"])
		logf("Uploaded %d symbol file(s).\n", results[0].Symbols)
	}

	var media *mediaState
	if len(asset.Images) > 0 || !asset.Media.empty() {
		m, err := uploadMediaSpec(client, asset.Media, asset.Images)
		if err != nil {
			return err
		}
		media = m
		if media != nil && !jsonOutput {
			printMedia(*media)
		}
	}

	logln("Upload completed successfully")
	if jsonOutput {
		out := map[string]any{"ok": true, "builds": results}
		if media != nil {
			out["media"] = media
		}
		return emit(out)
	}
	return nil
}

type slotImage struct {
	path string
	slot imageSlot
}

// buildImages pairs each image in a build spec with the slot games_service checks
// it against: the cover and thumbnail have their own sizes, and gallery images
// (including the legacy images list) are screenshots.
func buildImages(m *MediaSpec, legacyImages []string) []slotImage {
	var out []slotImage
	if m != nil {
		if m.Cover != "" {
			out = append(out, slotImage{m.Cover, slotCover})
		}
		if m.Thumbnail != "" {
			out = append(out, slotImage{m.Thumbnail, slotThumbnail})
		}
		for _, p := range m.Gallery {
			out = append(out, slotImage{p, slotScreenshot})
		}
	}
	for _, p := range legacyImages {
		out = append(out, slotImage{p, slotScreenshot})
	}
	return out
}
