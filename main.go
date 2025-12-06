package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const defaultAPIURL = "https://api.blazium.online/api/v1"

var (
	accessToken string
	secretKey   string
	apiURL      string
	assetFile   string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "cli",
	Short: "Blazium CLI tool for uploading builds and files",
	Long:  `A CLI tool for uploading game builds and files to the Blazium games service.`,
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

func init() {
	cobra.OnInitialize(initConfig)

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

	// Mark asset as required for both subcommands
	buildCmd.MarkFlagRequired("asset")
	addfilesCmd.MarkFlagRequired("asset")

	// Add subcommands
	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(addfilesCmd)
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
