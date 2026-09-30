package main

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// These mirror the Blazium Games services so bad input fails before any
// network call. They are copied, not imported (see deps_test.go).
// Upload sizing; variables so tests can shrink them.
var (
	singleShotMaxBytes int64 = 64 << 20
	chunkBytes         int64 = 16 << 20
)

const (
	maxBuildFileBytes = 5 << 30
	maxChunkTries     = 5
	maxSessionReopens = 3

	maxVersionLen     = 32
	maxTitleLen       = 255
	maxDescriptionLen = 10000
	maxChangelogItems = 100
	maxDemoURLLen     = 255

	maxImageBytes        = 10 << 20
	minImageSide         = 512
	maxImageSide         = 2048
	maxGalleryImages     = 20
	maxGalleryPerRequest = 10

	maxSymbolFileBytes   = 512 << 20
	maxSymbolUploadBytes = 1 << 30
	maxSymbolZipBytes    = 2 << 30
	maxSymbolsPerUpload  = 200
)

var (
	allowedOS       = []string{"windows", "macos", "linux", "android", "ios", "web"}
	allowedArch     = []string{"x86_64", "x86", "arm64", "arm32", "arm", "universal", "wasm32", "wasm"}
	channelRe       = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	engineVersionRe = regexp.MustCompile(`^\d+(\.\d+){0,3}(-[a-z0-9.]+)?$`)
	uidRe           = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

var (
	osHelp      = strings.Join(allowedOS, ", ")
	archHelp    = strings.Join(allowedArch, ", ")
	channelHelp = "1-32 lowercase letters, digits, - or _ (stable, beta, ...)"
)

// normalizePlatform applies the same aliases as the services (darwin -> macos, amd64 -> x86_64, ...).
func normalizePlatform(osName, arch, channel string) (string, string, string) {
	osName = strings.ToLower(strings.TrimSpace(osName))
	arch = strings.ToLower(strings.TrimSpace(arch))
	channel = strings.ToLower(strings.TrimSpace(channel))
	switch osName {
	case "win", "win32", "win64":
		osName = "windows"
	case "darwin", "osx", "mac":
		osName = "macos"
	}
	switch arch {
	case "amd64", "x64":
		arch = "x86_64"
	case "aarch64":
		arch = "arm64"
	}
	return osName, arch, channel
}

// validatePlatform normalizes and checks one os/arch/channel triple.
func validatePlatform(osName, arch, channel string) (PlatformSpec, error) {
	osName, arch, channel = normalizePlatform(osName, arch, channel)
	if channel == "" {
		channel = "stable"
	}
	if !slices.Contains(allowedOS, osName) {
		return PlatformSpec{}, usageErrorf("os %q is not supported; use one of %s", osName, osHelp)
	}
	if !slices.Contains(allowedArch, arch) {
		return PlatformSpec{}, usageErrorf("arch %q is not supported; use one of %s", arch, archHelp)
	}
	if !channelRe.MatchString(channel) {
		return PlatformSpec{}, usageErrorf("channel %q is invalid; it must be %s", channel, channelHelp)
	}
	return PlatformSpec{OS: osName, Arch: arch, Channel: channel}, nil
}

// validateFilter checks optional os/arch/channel filters (empty is allowed).
func validateFilter(osName, arch, channel string) (string, string, string, error) {
	osName, arch, channel = normalizePlatform(osName, arch, channel)
	if osName != "" && !slices.Contains(allowedOS, osName) {
		return "", "", "", usageErrorf("os must be one of %s", osHelp)
	}
	if arch != "" && !slices.Contains(allowedArch, arch) {
		return "", "", "", usageErrorf("arch must be one of %s", archHelp)
	}
	if channel != "" && !channelRe.MatchString(channel) {
		return "", "", "", usageErrorf("channel must be %s", channelHelp)
	}
	return osName, arch, channel, nil
}

func validateEngineVersion(v string) (string, error) {
	v = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "v")
	if v == "" {
		return "", nil
	}
	if len(v) > 32 || !engineVersionRe.MatchString(v) {
		return "", usageErrorf("engine_version %q must look like 4.3, 4.3.1 or 4.3.0-beta.2", v)
	}
	return v, nil
}

func validateDemoURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(raw) > maxDemoURLLen {
		return usageErrorf("video/demo_url must be an http or https URL of at most %d characters", maxDemoURLLen)
	}
	return nil
}

// validateBuildFields applies the /tool/upload/build limits.
func validateBuildFields(a *BuildAsset) error {
	switch {
	case len(a.Version) > maxVersionLen:
		return usageErrorf("version must be at most %d characters", maxVersionLen)
	case len(a.Title) > maxTitleLen:
		return usageErrorf("title must be at most %d characters", maxTitleLen)
	case len(a.Description) > maxDescriptionLen:
		return usageErrorf("description must be at most %d characters", maxDescriptionLen)
	case len(a.Changelog) > maxChangelogItems:
		return usageErrorf("changelog can have at most %d entries", maxChangelogItems)
	}
	return validateDemoURL(a.Video)
}

func validUID(s string) bool { return uidRe.MatchString(strings.TrimSpace(s)) }

func sizeText(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
