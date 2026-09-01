package main

import (
	"fmt"
	"os"
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
		return nil, fmt.Errorf("unsupported spec: %s", spec)
	}

	parser, exists := specParsers[version]
	if !exists {
		supportedVersions := GetSupportedVersions(spec)
		if len(supportedVersions) == 0 {
			return nil, fmt.Errorf("unsupported version: %s for spec '%s' (no versions registered for this spec)", version, spec)
		}
		return nil, fmt.Errorf("unsupported version: %s for spec '%s' (supported versions: %s)", version, spec, strings.Join(supportedVersions, ", "))
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

// BuildAsset represents the asset structure for build spec
type BuildAsset struct {
	Title       string           `yaml:"title"`
	Type        string           `yaml:"type"`
	Description string           `yaml:"description"`
	Version     string           `yaml:"version"`
	OS          string           `yaml:"os,omitempty"`
	Arch        string           `yaml:"arch,omitempty"`
	Channel     string           `yaml:"channel,omitempty"`
	Platforms   []PlatformSpec   `yaml:"platforms,omitempty"`
	Video       string           `yaml:"video,omitempty"`
	Images      []string         `yaml:"images,omitempty"`
	Changelog   []ChangelogEntry `yaml:"changelog,omitempty"`
}

// FileEntry represents a single file entry in the files list
type FileEntry struct {
	File string `yaml:"file"`
}

// FilesAsset represents the asset structure for addfiles spec
type FilesAsset struct {
	Type    string      `yaml:"type"`
	Version string      `yaml:"version"`
	Channel string      `yaml:"channel"`
	OS      string      `yaml:"os"`
	Arch    string      `yaml:"arch"`
	Files   []FileEntry `yaml:"files"`
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
		return nil, fmt.Errorf("missing required field: asset.title")
	}
	if buildAsset.Type == "" {
		return nil, fmt.Errorf("missing required field: asset.type")
	}
	if buildAsset.Description == "" {
		return nil, fmt.Errorf("missing required field: asset.description")
	}
	if buildAsset.Version == "" {
		return nil, fmt.Errorf("missing required field: asset.version")
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
	if filesAsset.Type == "" {
		return nil, fmt.Errorf("missing required field: asset.type")
	}
	if filesAsset.Version == "" {
		return nil, fmt.Errorf("missing required field: asset.version")
	}
	if filesAsset.Channel == "" {
		return nil, fmt.Errorf("missing required field: asset.channel")
	}
	if filesAsset.OS == "" {
		return nil, fmt.Errorf("missing required field: asset.os")
	}
	if filesAsset.Arch == "" {
		return nil, fmt.Errorf("missing required field: asset.arch")
	}
	if len(filesAsset.Files) == 0 {
		return nil, fmt.Errorf("missing required field: asset.files (must have at least one file)")
	}

	parsed.FilesAsset = &filesAsset
	return parsed, nil
}

// ParseYAML parses the YAML file and returns a ParsedConfig
func ParseYAML(filename string) (*ParsedConfig, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Validate spec
	if config.Spec != "build" && config.Spec != "addfiles" {
		return nil, fmt.Errorf("invalid spec: %s (expected 'build' or 'addfiles')", config.Spec)
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
