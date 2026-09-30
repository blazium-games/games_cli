package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// VersionParser defines the interface for version-specific parsers
type VersionParser interface {
	ParseAsset(asset interface{}) (*ParsedConfig, error)
}

// parserRegistry stores registered parsers by spec and version
var parserRegistry = make(map[string]map[string]VersionParser)

// RegisterParser registers a parser for a specific spec and version
func RegisterParser(spec, version string, parser VersionParser) {
	if parserRegistry[spec] == nil {
		parserRegistry[spec] = make(map[string]VersionParser)
	}
	parserRegistry[spec][version] = parser
}

// GetParser retrieves a parser for a specific spec and version
func GetParser(spec, version string) (VersionParser, error) {
	specParsers, exists := parserRegistry[spec]
	if !exists {
		return nil, usageErrorf("unsupported spec: %s", spec)
	}

	parser, exists := specParsers[version]
	if !exists {
		supportedVersions := GetSupportedVersions(spec)
		if len(supportedVersions) == 0 {
			return nil, usageErrorf("unsupported version: %s for spec '%s' (no versions registered for this spec)", version, spec)
		}
		return nil, usageErrorf("unsupported version: %s for spec '%s' (supported versions: %s)", version, spec, strings.Join(supportedVersions, ", "))
	}

	return parser, nil
}

// GetSupportedVersions returns a list of supported versions for a given spec
func GetSupportedVersions(spec string) []string {
	specParsers, exists := parserRegistry[spec]
	if !exists {
		return nil
	}

	versions := make([]string, 0, len(specParsers))
	for version := range specParsers {
		versions = append(versions, version)
	}
	return versions
}

// Config represents the top-level YAML structure
type Config struct {
	Version string      `yaml:"version"`
	Spec    string      `yaml:"spec"`
	Asset   interface{} `yaml:"asset"`
}

// ChangelogEntry represents a single changelog entry
type ChangelogEntry struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
}

// MediaSpec lists store page images uploaded through /tool/media.
type MediaSpec struct {
	Cover     string   `yaml:"cover,omitempty"`
	Thumbnail string   `yaml:"thumbnail,omitempty"`
	Gallery   []string `yaml:"gallery,omitempty"`
}

func (m *MediaSpec) empty() bool {
	return m == nil || (m.Cover == "" && m.Thumbnail == "" && len(m.Gallery) == 0)
}

// AppSpec names one app of a project, such as a game's level editor. Leave it
// out for the project's main app.
type AppSpec struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name,omitempty"`
}

var appIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// validateApp normalizes the app id; a nil or empty app is the main app.
func validateApp(a *AppSpec) error {
	if a == nil {
		return nil
	}
	a.ID, a.Name = strings.ToLower(strings.TrimSpace(a.ID)), strings.TrimSpace(a.Name)
	if a.ID == "" {
		if a.Name != "" {
			return usageErrorf("asset.app.name needs asset.app.id")
		}
		return nil
	}
	if !appIDRe.MatchString(a.ID) {
		return usageErrorf("asset.app.id must be 1-32 lowercase letters, digits or dashes (got %q)", a.ID)
	}
	if len([]rune(a.Name)) > 80 {
		return usageErrorf("asset.app.name is limited to 80 characters")
	}
	return nil
}

func (a *AppSpec) id() string {
	if a == nil {
		return ""
	}
	return a.ID
}

// addTo sets app and app_name on a build request when the build belongs to a non-main app.
func (a *AppSpec) addTo(req map[string]any) {
	if a.id() == "" {
		return
	}
	req["app"] = a.ID
	if a.Name != "" {
		req["app_name"] = a.Name
	}
}

// appFromFlags applies --app and --app-name over the file's app.
func appFromFlags(current *AppSpec, id, name string) *AppSpec {
	if id == "" && name == "" {
		return current
	}
	out := &AppSpec{}
	if current != nil {
		*out = *current
	}
	if id != "" {
		out.ID = id
	}
	if name != "" {
		out.Name = name
	}
	return out
}

// BuildAsset represents the asset structure for build spec
type BuildAsset struct {
	Title string   `yaml:"title"`
	Type  string   `yaml:"type"`
	App   *AppSpec `yaml:"app,omitempty"`
	// AssetType is a deprecated alias for Type.
	AssetType     string         `yaml:"asset_type,omitempty"`
	Description   string         `yaml:"description"`
	Version       string         `yaml:"version"`
	EngineVersion string         `yaml:"engine_version,omitempty"`
	OS            string         `yaml:"os,omitempty"`
	Arch          string         `yaml:"arch,omitempty"`
	Channel       string         `yaml:"channel,omitempty"`
	Platforms     []PlatformSpec `yaml:"platforms,omitempty"`
	Video         string         `yaml:"video,omitempty"`
	// Images are added to the gallery; prefer media.gallery.
	Images    []string         `yaml:"images,omitempty"`
	Media     *MediaSpec       `yaml:"media,omitempty"`
	Symbols   string           `yaml:"symbols,omitempty"`
	Changelog []ChangelogEntry `yaml:"changelog,omitempty"`
}

// FileEntry represents a single file entry in the files list
type FileEntry struct {
	File string `yaml:"file"`
}

// FilesAsset represents the asset structure for addfiles spec
type FilesAsset struct {
	Type string   `yaml:"type"`
	App  *AppSpec `yaml:"app,omitempty"`
	// AssetType is a deprecated alias for Type.
	AssetType     string      `yaml:"asset_type,omitempty"`
	Version       string      `yaml:"version"`
	EngineVersion string      `yaml:"engine_version,omitempty"`
	Channel       string      `yaml:"channel"`
	OS            string      `yaml:"os"`
	Arch          string      `yaml:"arch"`
	Symbols       string      `yaml:"symbols,omitempty"`
	Files         []FileEntry `yaml:"files"`
}

func warnAssetType(assetType, typ string) string {
	if assetType == "" {
		return typ
	}
	fmt.Fprintln(stderr, "Warning: asset.asset_type is deprecated; use asset.type.")
	if typ == "" {
		return assetType
	}
	return typ
}

// ParsedConfig holds the parsed configuration with typed asset
type ParsedConfig struct {
	Version    string
	Spec       string
	BuildAsset *BuildAsset
	FilesAsset *FilesAsset
}

// BuildV1Parser handles parsing for build spec version v1
type BuildV1Parser struct{}

// ParseAsset parses the asset section for build v1
func (p *BuildV1Parser) ParseAsset(asset interface{}) (*ParsedConfig, error) {
	parsed := &ParsedConfig{
		Version: "v1",
		Spec:    "build",
	}

	var buildAsset BuildAsset
	assetData, err := yaml.Marshal(asset)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal asset: %w", err)
	}
	if err := yaml.Unmarshal(assetData, &buildAsset); err != nil {
		return nil, fmt.Errorf("failed to parse build asset: %w", err)
	}

	// Validate required fields
	if buildAsset.Title == "" {
		return nil, usageErrorf("missing required field: asset.title")
	}
	buildAsset.Type = warnAssetType(buildAsset.AssetType, buildAsset.Type)
	if buildAsset.Type == "" {
		return nil, usageErrorf("missing required field: asset.type")
	}
	if buildAsset.Description == "" {
		return nil, usageErrorf("missing required field: asset.description")
	}
	if buildAsset.Version == "" {
		return nil, usageErrorf("missing required field: asset.version")
	}
	if err := validateBuildAsset(&buildAsset); err != nil {
		return nil, err
	}

	parsed.BuildAsset = &buildAsset
	return parsed, nil
}

// AddFilesV1Parser handles parsing for addfiles spec version v1
type AddFilesV1Parser struct{}

// ParseAsset parses the asset section for addfiles v1
func (p *AddFilesV1Parser) ParseAsset(asset interface{}) (*ParsedConfig, error) {
	parsed := &ParsedConfig{
		Version: "v1",
		Spec:    "addfiles",
	}

	var filesAsset FilesAsset
	assetData, err := yaml.Marshal(asset)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal asset: %w", err)
	}
	if err := yaml.Unmarshal(assetData, &filesAsset); err != nil {
		return nil, fmt.Errorf("failed to parse files asset: %w", err)
	}

	// Validate required fields
	filesAsset.Type = warnAssetType(filesAsset.AssetType, filesAsset.Type)
	if filesAsset.Type == "" {
		return nil, usageErrorf("missing required field: asset.type")
	}
	if filesAsset.Version == "" {
		return nil, usageErrorf("missing required field: asset.version")
	}
	if filesAsset.Channel == "" {
		return nil, usageErrorf("missing required field: asset.channel")
	}
	if filesAsset.OS == "" {
		return nil, usageErrorf("missing required field: asset.os")
	}
	if filesAsset.Arch == "" {
		return nil, usageErrorf("missing required field: asset.arch")
	}
	if len(filesAsset.Files) == 0 {
		return nil, usageErrorf("missing required field: asset.files (must have at least one file)")
	}
	if err := validateFilesAsset(&filesAsset); err != nil {
		return nil, err
	}

	parsed.FilesAsset = &filesAsset
	return parsed, nil
}

// validateBuildAsset normalizes and checks everything in a build spec that
// can be checked without the network.
func validateBuildAsset(a *BuildAsset) error {
	if err := validateBuildFields(a); err != nil {
		return err
	}
	if err := validateApp(a.App); err != nil {
		return err
	}
	ev, err := validateEngineVersion(a.EngineVersion)
	if err != nil {
		return err
	}
	a.EngineVersion = ev
	for i, p := range a.Platforms {
		if p.OS == "" {
			return usageErrorf("asset.platforms[%d].os is required (%s)", i, osHelp)
		}
		if p.Arch == "" {
			p.Arch = "x86_64"
		}
		spec, err := validatePlatform(p.OS, p.Arch, p.Channel)
		if err != nil {
			return usageErrorf("asset.platforms[%d]: %v", i, err)
		}
		a.Platforms[i] = spec
	}
	if a.OS != "" {
		arch := a.Arch
		if arch == "" {
			arch = "x86_64"
		}
		spec, err := validatePlatform(a.OS, arch, a.Channel)
		if err != nil {
			return err
		}
		a.OS, a.Arch, a.Channel = spec.OS, spec.Arch, spec.Channel
	}
	if n := len(a.Images) + galleryLen(a.Media); n > maxGalleryImages {
		return usageErrorf("images and media.gallery list %d images; the gallery holds at most %d", n, maxGalleryImages)
	}
	return nil
}

func galleryLen(m *MediaSpec) int {
	if m == nil {
		return 0
	}
	return len(m.Gallery)
}

func validateFilesAsset(a *FilesAsset) error {
	if len(a.Version) > maxVersionLen {
		return usageErrorf("version must be at most %d characters", maxVersionLen)
	}
	if err := validateApp(a.App); err != nil {
		return err
	}
	ev, err := validateEngineVersion(a.EngineVersion)
	if err != nil {
		return err
	}
	a.EngineVersion = ev
	spec, err := validatePlatform(a.OS, a.Arch, a.Channel)
	if err != nil {
		return err
	}
	a.OS, a.Arch, a.Channel = spec.OS, spec.Arch, spec.Channel
	return nil
}

// ParseYAML parses the YAML file and returns a ParsedConfig
func ParseYAML(filename string) (*ParsedConfig, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, usageErrorf("failed to read %s: %v", filename, err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, usageErrorf("failed to parse YAML: %v", err)
	}

	// Validate spec
	if config.Spec != "build" && config.Spec != "addfiles" {
		return nil, usageErrorf("invalid spec: %s (expected 'build' or 'addfiles')", config.Spec)
	}

	// Get the appropriate parser for this spec and version
	parser, err := GetParser(config.Spec, config.Version)
	if err != nil {
		return nil, err
	}

	// Parse the asset using the version-specific parser
	parsed, err := parser.ParseAsset(config.Asset)
	if err != nil {
		return nil, err
	}

	return parsed, nil
}

// ReadYAMLFile reads a YAML file and returns the Config struct
func ReadYAMLFile(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	return &config, nil
}

// WriteYAMLFile writes a Config struct to a YAML file
func WriteYAMLFile(filename string, config *Config) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// init registers the default v1 parsers
func init() {
	// Register build v1 parser
	RegisterParser("build", "v1", &BuildV1Parser{})

	// Register addfiles v1 parser
	RegisterParser("addfiles", "v1", &AddFilesV1Parser{})
}
