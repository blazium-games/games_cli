package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

const defaultAPIURL = "https://api.blazium.online/api/v1"
const defaultUploadURL = "https://uploader.blazium.online/api/v1"

var version = "dev"

var (
	accessToken string
	secretKey   string
	apiURL      string
	assetFile   string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "chauffeur",
	Short: "Deprecated: use blazium-cli games (formerly chauffeur)",
	Long:  `Deprecated. Use blazium-cli games build|addfiles|genbuild|addchangelog|setfiles. This chauffeur binary is a compatibility alias only.`,
}

// buildCmd represents the build command
var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Upload a build using a YAML configuration file",
	Long:  `Upload a build to the Blazium service using a YAML configuration file. The YAML file must have spec: "build" and contain build asset information.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Get values from Viper (flags > env vars > defaults)
		accessToken := viper.GetString("access")
		secretKey := viper.GetString("secret")
		apiURL := viper.GetString("url")
		assetFile, _ := cmd.Flags().GetString("asset")

		// Validate required fields
		if assetFile == "" {
			return fmt.Errorf("--asset flag is required")
		}
		if accessToken == "" {
			return fmt.Errorf("access token is required (use --access flag or set BLAZIUM_ACCESS_TOKEN env var)")
		}
		if secretKey == "" {
			return fmt.Errorf("secret key is required (use --secret flag or set BLAZIUM_SECRET_KEY env var)")
		}

		client := NewClient(apiURL, accessToken, secretKey)
		client.SetUploadURL(viper.GetString("upload"))

		config, err := ParseYAML(assetFile)
		if err != nil {
			return fmt.Errorf("error parsing YAML file: %w", err)
		}
		if config.Spec != "build" {
			return fmt.Errorf("invalid spec type '%s' in YAML file, expected 'build'", config.Spec)
		}
		osFlag, _ := cmd.Flags().GetString("os")
		archFlag, _ := cmd.Flags().GetString("arch")
		channelFlag, _ := cmd.Flags().GetString("channel")
		if osFlag != "" || archFlag != "" || channelFlag != "" {
			config.BuildAsset.Platforms = nil
			if osFlag != "" {
				config.BuildAsset.OS = osFlag
			}
			if archFlag != "" {
				config.BuildAsset.Arch = archFlag
			}
			if channelFlag != "" {
				config.BuildAsset.Channel = channelFlag
			}
		}

		// Process build
		if err := ProcessBuild(client, config); err != nil {
			return fmt.Errorf("error processing build: %w", err)
		}

		fmt.Println("Upload completed successfully")
		return nil
	},
}

// addfilesCmd represents the addfiles command
var addfilesCmd = &cobra.Command{
	Use:   "addfiles",
	Short: "Upload files using a YAML configuration file",
	Long: `Upload files to the Blazium service using a YAML configuration file.
The YAML file must have spec: "addfiles" and contain file asset information.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Get values from Viper (flags > env vars > defaults)
		accessToken := viper.GetString("access")
		secretKey := viper.GetString("secret")
		apiURL := viper.GetString("url")
		assetFile, _ := cmd.Flags().GetString("asset")

		// Validate required fields
		if assetFile == "" {
			return fmt.Errorf("--asset flag is required")
		}
		if accessToken == "" {
			return fmt.Errorf("access token is required (use --access flag or set BLAZIUM_ACCESS_TOKEN env var)")
		}
		if secretKey == "" {
			return fmt.Errorf("secret key is required (use --secret flag or set BLAZIUM_SECRET_KEY env var)")
		}

		client := NewClient(apiURL, accessToken, secretKey)
		client.SetUploadURL(viper.GetString("upload"))

		config, err := ParseYAML(assetFile)
		if err != nil {
			return fmt.Errorf("error parsing YAML file: %w", err)
		}

		// Validate spec type
		if config.Spec != "addfiles" {
			return fmt.Errorf("invalid spec type '%s' in YAML file, expected 'addfiles'", config.Spec)
		}
		if osFlag, _ := cmd.Flags().GetString("os"); osFlag != "" {
			config.FilesAsset.OS = osFlag
		}
		if archFlag, _ := cmd.Flags().GetString("arch"); archFlag != "" {
			config.FilesAsset.Arch = archFlag
		}
		if channelFlag, _ := cmd.Flags().GetString("channel"); channelFlag != "" {
			config.FilesAsset.Channel = channelFlag
		}

		// Process files
		if err := ProcessFiles(client, config); err != nil {
			return fmt.Errorf("error processing files: %w", err)
		}

		fmt.Println("Upload completed successfully")
		return nil
	},
}

// genbuildCmd represents the genbuild command
var genbuildCmd = &cobra.Command{
	Use:   "genbuild",
	Short: "Generate a new build.yml file",
	Long:  `Generate a new build.yml file with default values. Optionally scan a directory for images and set a version.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		imagesDir, _ := cmd.Flags().GetString("images")
		version, _ := cmd.Flags().GetString("version")

		// Default version if not provided
		if version == "" {
			version = "0.0.1"
		}

		// Scan images directory if provided
		var imagePaths []string
		if imagesDir != "" {
			images, err := ScanImageDirectory(imagesDir)
			if err != nil {
				return fmt.Errorf("error scanning images directory: %w", err)
			}
			imagePaths = images
			fmt.Printf("Found %d image(s) in directory\n", len(imagePaths))
		}

		// Create default build asset
		buildAsset := BuildAsset{
			Title:       "Untitled Build",
			Type:        "game",
			Description: "No description provided",
			Version:     version,
			Images:      imagePaths,
			Changelog:   []ChangelogEntry{},
		}

		// Create config
		config := &Config{
			Version: "v1",
			Spec:    "build",
			Asset:   buildAsset,
		}

		// Write to build.yml
		if err := WriteYAMLFile("build.yml", config); err != nil {
			return fmt.Errorf("error writing build.yml: %w", err)
		}

		fmt.Println("Successfully generated build.yml")
		return nil
	},
}

// addchangelogCmd represents the addchangelog command
var addchangelogCmd = &cobra.Command{
	Use:   "addchangelog",
	Short: "Add a changelog entry to build.yml",
	Long:  `Add a changelog entry to an existing build.yml file. The entry will be appended to the end of the changelog list.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		title, _ := cmd.Flags().GetString("title")
		description, _ := cmd.Flags().GetString("description")

		// Validate required fields
		if title == "" {
			return fmt.Errorf("--title flag is required")
		}
		if description == "" {
			return fmt.Errorf("--description flag is required")
		}

		// Read existing build.yml
		config, err := ReadYAMLFile("build.yml")
		if err != nil {
			return fmt.Errorf("error reading build.yml: %w", err)
		}

		// Validate spec type
		if config.Spec != "build" {
			return fmt.Errorf("invalid spec type '%s' in build.yml, expected 'build'", config.Spec)
		}

		// Parse the asset to get the build asset
		assetData, err := yaml.Marshal(config.Asset)
		if err != nil {
			return fmt.Errorf("failed to marshal asset: %w", err)
		}

		var buildAsset BuildAsset
		if err := yaml.Unmarshal(assetData, &buildAsset); err != nil {
			return fmt.Errorf("failed to parse build asset: %w", err)
		}

		// Add new changelog entry
		newEntry := ChangelogEntry{
			Title:       title,
			Description: description,
		}
		buildAsset.Changelog = append(buildAsset.Changelog, newEntry)

		// Update config with modified asset
		config.Asset = buildAsset

		// Write back to build.yml
		if err := WriteYAMLFile("build.yml", config); err != nil {
			return fmt.Errorf("error writing build.yml: %w", err)
		}

		fmt.Println("Successfully added changelog entry to build.yml")
		return nil
	},
}

// setfilesCmd represents the setfiles command
var setfilesCmd = &cobra.Command{
	Use:   "setfiles",
	Short: "Generate a new addfiles.yml file",
	Long:  `Generate a new addfiles.yml file with default values. Optionally scan a directory for files and set various configuration options.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		version, _ := cmd.Flags().GetString("version")
		channel, _ := cmd.Flags().GetString("channel")
		osType, _ := cmd.Flags().GetString("os")
		assetType, _ := cmd.Flags().GetString("type")
		arch, _ := cmd.Flags().GetString("arch")
		filesDir, _ := cmd.Flags().GetString("files")

		// Set defaults if not provided
		if version == "" {
			version = "0.0.1"
		}
		if channel == "" {
			channel = "stable"
		}
		if osType == "" {
			osType = "windows"
		}
		if assetType == "" {
			assetType = "game"
		}
		if arch == "" {
			arch = "x86_64"
		}

		// Scan files directory if provided
		var fileEntries []FileEntry
		if filesDir != "" {
			files, err := ScanFileDirectory(filesDir)
			if err != nil {
				return fmt.Errorf("error scanning files directory: %w", err)
			}
			for _, file := range files {
				fileEntries = append(fileEntries, FileEntry{File: file})
			}
			fmt.Printf("Found %d file(s) in directory\n", len(fileEntries))
		}

		// Create default files asset
		filesAsset := FilesAsset{
			Type:    assetType,
			Version: version,
			Channel: channel,
			OS:      osType,
			Arch:    arch,
			Files:   fileEntries,
		}

		// Create config
		config := &Config{
			Version: "v1",
			Spec:    "addfiles",
			Asset:   filesAsset,
		}

		// Write to addfiles.yml
		if err := WriteYAMLFile("addfiles.yml", config); err != nil {
			return fmt.Errorf("error writing addfiles.yml: %w", err)
		}

		fmt.Println("Successfully generated addfiles.yml")
		return nil
	},
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.Version = version

	// Persistent flags (available to all subcommands)
	rootCmd.PersistentFlags().StringVarP(&accessToken, "access", "", "", "Blazium access token (overrides BLAZIUM_ACCESS_TOKEN env var)")
	rootCmd.PersistentFlags().StringVarP(&secretKey, "secret", "", "", "Blazium secret key (overrides BLAZIUM_SECRET_KEY env var)")
	rootCmd.PersistentFlags().StringVarP(&apiURL, "url", "", defaultAPIURL, "API base URL (overrides BLAZIUM_API_URL env var)")
	rootCmd.PersistentFlags().String("upload", defaultUploadURL, "Upload service base URL (overrides BLAZIUM_UPLOAD_URL env var)")

	viper.BindPFlag("access", rootCmd.PersistentFlags().Lookup("access"))
	viper.BindPFlag("secret", rootCmd.PersistentFlags().Lookup("secret"))
	viper.BindPFlag("url", rootCmd.PersistentFlags().Lookup("url"))
	viper.BindPFlag("upload", rootCmd.PersistentFlags().Lookup("upload"))

	// Flags for subcommands
	buildCmd.Flags().StringVarP(&assetFile, "asset", "", "", "Path to YAML asset file (required)")
	buildCmd.Flags().String("os", "", "OS for this build_id (windows, linux, macos). Overrides YAML; use in CI matrices.")
	buildCmd.Flags().String("arch", "", "Arch for this build_id (x86_64, arm64). Overrides YAML.")
	buildCmd.Flags().String("channel", "", "Channel for this build_id (stable, beta). Overrides YAML.")
	addfilesCmd.Flags().StringVarP(&assetFile, "asset", "", "", "Path to YAML asset file (required)")
	addfilesCmd.Flags().String("os", "", "OS for this build_id. Overrides YAML.")
	addfilesCmd.Flags().String("arch", "", "Arch for this build_id. Overrides YAML.")
	addfilesCmd.Flags().String("channel", "", "Channel for this build_id. Overrides YAML.")
	genbuildCmd.Flags().String("images", "", "Directory containing images to scan and add (optional)")
	genbuildCmd.Flags().String("version", "", "Version to set for the build (optional, defaults to 0.0.1)")
	addchangelogCmd.Flags().String("title", "", "Title for the changelog entry (required)")
	addchangelogCmd.Flags().String("description", "", "Description for the changelog entry (required)")
	setfilesCmd.Flags().String("version", "", "Version to set for the files (optional, defaults to 0.0.1)")
	setfilesCmd.Flags().String("channel", "", "Channel to set (optional, defaults to stable)")
	setfilesCmd.Flags().String("os", "", "OS to set (optional, defaults to windows)")
	setfilesCmd.Flags().String("type", "", "Type to set (optional, defaults to game)")
	setfilesCmd.Flags().String("arch", "", "Architecture to set (optional, defaults to x86_64)")
	setfilesCmd.Flags().String("files", "", "Directory containing files to scan and add (optional)")

	// Mark asset as required for both subcommands
	buildCmd.MarkFlagRequired("asset")
	addfilesCmd.MarkFlagRequired("asset")
	addchangelogCmd.MarkFlagRequired("title")
	addchangelogCmd.MarkFlagRequired("description")

	// Add subcommands
	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(addfilesCmd)
	rootCmd.AddCommand(genbuildCmd)
	rootCmd.AddCommand(addchangelogCmd)
	rootCmd.AddCommand(setfilesCmd)
}

// initConfig reads in environment variables and config file if set
func initConfig() {
	// Set environment variable prefix
	viper.SetEnvPrefix("BLAZIUM")

	// Bind environment variables
	viper.BindEnv("access", "BLAZIUM_ACCESS_TOKEN")
	viper.BindEnv("secret", "BLAZIUM_SECRET_KEY")
	viper.BindEnv("url", "BLAZIUM_API_URL")
	viper.BindEnv("upload", "BLAZIUM_UPLOAD_URL")

	viper.SetDefault("url", defaultAPIURL)
	viper.SetDefault("upload", defaultUploadURL)

	// Read environment variables
	viper.AutomaticEnv()
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
