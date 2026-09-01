package main

import "fmt"

// PlatformSpec is one OS/arch/channel triple. Each gets its own build_id.
type PlatformSpec struct {
	OS      string `yaml:"os"`
	Arch    string `yaml:"arch"`
	Channel string `yaml:"channel,omitempty"`
}

func buildPlatforms(asset *BuildAsset) []PlatformSpec {
	if asset == nil {
		return nil
	}
	if len(asset.Platforms) > 0 {
		out := make([]PlatformSpec, 0, len(asset.Platforms))
		for _, p := range asset.Platforms {
			if p.OS == "" {
				continue
			}
			if p.Arch == "" {
				p.Arch = "x86_64"
			}
			if p.Channel == "" {
				p.Channel = "stable"
			}
			out = append(out, p)
		}
		if len(out) > 0 {
			return out
		}
	}
	if asset.OS != "" {
		arch := asset.Arch
		if arch == "" {
			arch = "x86_64"
		}
		ch := asset.Channel
		if ch == "" {
			ch = "stable"
		}
		return []PlatformSpec{{OS: asset.OS, Arch: arch, Channel: ch}}
	}
	return []PlatformSpec{{}}
}

func responseBuildID(data map[string]interface{}) string {
	if data == nil {
		return ""
	}
	if id, _ := data["build_id"].(string); id != "" {
		return id
	}
	id, _ := data["build_uid"].(string)
	return id
}

func printBuildIDs(data map[string]interface{}, p PlatformSpec) {
	buildID := responseBuildID(data)
	appID, _ := data["app_id"].(string)
	fmt.Println("Build ready for crash reporters and CI.")
	if p.OS != "" {
		fmt.Printf("  platform: %s/%s", p.OS, p.Arch)
		if p.Channel != "" {
			fmt.Printf("  channel=%s", p.Channel)
		}
		fmt.Println()
	}
	if appID != "" {
		fmt.Printf("  app_id:   %s\n", appID)
		fmt.Printf("  BLAZIUM_GAMES_APP_ID=%s\n", appID)
	}
	fmt.Printf("  build_id: %s\n", buildID)
	fmt.Printf("  BLAZIUM_GAMES_BUILD_ID=%s\n", buildID)
	fmt.Printf("  Crash reporter: X-App-Id=%s X-Build-Id=%s\n", appID, buildID)
}
