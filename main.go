package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

const defaultAPIURL = "https://api.blazium.online/api/v1"

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
	Short: "Chauffeur CLI for uploading builds and files",
	Long:  `Chauffeur uploads game builds and files to the Blazium games service.`,
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

		// Create HTTP client
		client := NewClient(apiURL, accessToken, secretKey)

		// Parse YAML file
		config, err := ParseYAML(assetFile)
		if err != nil {
			return fmt.Errorf("error parsing YAML file: %w", err)
		}

		// Validate spec type
		if config.Spec != "build" {
			return fmt.Errorf("invalid spec type '%s' in YAML file, expected 'build'", config.Spec)
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

		// Create HTTP client
		client := NewClient(apiURL, accessToken, secretKey)

		// Parse YAML file
		config, err := ParseYAML(assetFile)
		if err != nil {
			return fmt.Errorf("error parsing YAML file: %w", err)
		}

		// Validate spec type
		if config.Spec != "addfiles" {
			return fmt.Errorf("invalid spec type '%s' in YAML file, expected 'addfiles'", config.Spec)
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

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.Version = version

	// Persistent flags (available to all subcommands)
	rootCmd.PersistentFlags().StringVarP(&accessToken, "access", "", "", "Blazium access token (overrides BLAZIUM_ACCESS_TOKEN env var)")
	rootCmd.PersistentFlags().StringVarP(&secretKey, "secret", "", "", "Blazium secret key (overrides BLAZIUM_SECRET_KEY env var)")
	rootCmd.PersistentFlags().StringVarP(&apiURL, "url", "", defaultAPIURL, "API base URL (overrides BLAZIUM_API_URL env var)")

	// Bind flags to Viper
	viper.BindPFlag("access", rootCmd.PersistentFlags().Lookup("access"))
	viper.BindPFlag("secret", rootCmd.PersistentFlags().Lookup("secret"))
	viper.BindPFlag("url", rootCmd.PersistentFlags().Lookup("url"))

	// Flags for subcommands
	buildCmd.Flags().StringVarP(&assetFile, "asset", "", "", "Path to YAML asset file (required)")
	addfilesCmd.Flags().StringVarP(&assetFile, "asset", "", "", "Path to YAML asset file (required)")
	genbuildCmd.Flags().String("images", "", "Directory containing images to scan and add (optional)")
	genbuildCmd.Flags().String("version", "", "Version to set for the build (optional, defaults to 0.0.1)")
	addchangelogCmd.Flags().String("title", "", "Title for the changelog entry (required)")
	addchangelogCmd.Flags().String("description", "", "Description for the changelog entry (required)")

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
}

// initConfig reads in environment variables and config file if set
func initConfig() {
	// Set environment variable prefix
	viper.SetEnvPrefix("BLAZIUM")

	// Bind environment variables
	viper.BindEnv("access", "BLAZIUM_ACCESS_TOKEN")
	viper.BindEnv("secret", "BLAZIUM_SECRET_KEY")
	viper.BindEnv("url", "BLAZIUM_API_URL")

	// Set default for URL
	viper.SetDefault("url", defaultAPIURL)

	// Read environment variables
	viper.AutomaticEnv()
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
