package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

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

const authHelp = `Authentication uses the game's deploy key, issued on the game's dashboard
(or through MCP request_deploy_key):

  BLAZIUM_ACCESS_TOKEN   access token (or --access)
  BLAZIUM_SECRET_KEY     secret key (or --secret-stdin; avoid --secret)

Issuing a new deploy key invalidates the previous one.`

var rootCmd = &cobra.Command{
	Use:   "chauffeur",
	Short: "Upload builds, symbols and store images to Blazium Games",
	Long: `chauffeur is the Blazium Games upload tool. Everything that sends files to the
store goes through it: builds, per-platform files, Breakpad symbols and store
page images. The website and MCP can view and delete these, but not upload.

` + authHelp + `

Exit codes: 0 success, 1 usage or validation error, 2 API error, 3 network
error after retries. With --json, the result (or error) is one JSON object on
stdout and progress goes to stderr.

Docs: https://docs.blazium.games/docs/cli`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// addAppFlags registers --app and --app-name, which pick the project's app
// (for example a dedicated server) that the build belongs to.
func addAppFlags(cmd *cobra.Command) {
	cmd.Flags().String("app", "", "App id within the project: 1-32 lowercase letters, digits or dashes (default: the main app)")
	cmd.Flags().String("app-name", "", "Display name for the app, shown on the downloads page (needs an app id)")
}

func appFromCmd(cmd *cobra.Command, current *AppSpec) (*AppSpec, error) {
	id, _ := cmd.Flags().GetString("app")
	name, _ := cmd.Flags().GetString("app-name")
	if strings.TrimSpace(name) != "" && strings.TrimSpace(id) == "" && strings.TrimSpace(current.id()) == "" {
		return nil, usageErrorf("--app-name needs --app (or asset.app.id in the file)")
	}
	return appFromFlags(current, id, name), nil
}

const platformFlagHelp = "\n\nPlatform values:\n  os       " + "windows, macos, linux, android, ios, web" +
	"\n  arch     x86_64, x86, arm64, arm32, arm, universal, wasm32, wasm" +
	"\n  channel  1-32 lowercase letters, digits, - or _ (default stable)"

var buildCmd = &cobra.Command{
	Use:   "build --asset build.yml",
	Short: "Create builds (one build_id per platform) from a build.yml",
	Long: `Create builds from a build.yml (spec: build). Each platform in the file gets
its own build_id, printed for crash reporters and CI. --os/--arch/--channel
replace the file's platforms with a single one, for CI matrices.

The file can also set engine_version, media (cover, thumbnail, gallery) and
symbols. Symbols need exactly one platform. Everything is validated locally
before anything is sent.` + platformFlagHelp + "\n\n" + authHelp,
	Example: `  chauffeur build --asset build.yml
  chauffeur build --asset build.yml --os windows --arch x86_64 --symbols build/symbols
  chauffeur build --asset build.yml --engine-version 4.3 --json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		assetFile, _ := cmd.Flags().GetString("asset")
		config, err := ParseYAML(assetFile)
		if err != nil {
			return err
		}
		if config.Spec != "build" {
			return usageErrorf("%s has spec %q; chauffeur build needs spec: build", assetFile, config.Spec)
		}
		a := config.BuildAsset
		osFlag, _ := cmd.Flags().GetString("os")
		archFlag, _ := cmd.Flags().GetString("arch")
		channelFlag, _ := cmd.Flags().GetString("channel")
		if osFlag != "" || archFlag != "" || channelFlag != "" {
			a.Platforms = nil
			if osFlag != "" {
				a.OS = osFlag
			}
			if archFlag != "" {
				a.Arch = archFlag
			}
			if channelFlag != "" {
				a.Channel = channelFlag
			}
			if a.OS == "" {
				return usageErrorf("--arch and --channel need --os (or os in %s)", assetFile)
			}
		}
		if v, _ := cmd.Flags().GetString("engine-version"); v != "" {
			a.EngineVersion = v
		}
		if v, _ := cmd.Flags().GetString("symbols"); v != "" {
			a.Symbols = v
		}
		if a.App, err = appFromCmd(cmd, a.App); err != nil {
			return err
		}
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		return ProcessBuild(client, config)
	},
}

var addfilesCmd = &cobra.Command{
	Use:   "addfiles --asset addfiles.yml",
	Short: "Create a platform build and upload its files from an addfiles.yml",
	Long: `Create one platform build and upload its files (spec: addfiles). The listed
files are zipped, checksummed and uploaded: in one streamed request up to
64 MB, otherwise in 16 MB chunks that resume after a dropped connection and
retry rate limits and server errors with backoff. Build files can be up to
5 GB.

If a build with the same version, type, OS, arch and channel already exists
(for example from chauffeur build), the files are added to it and its title,
description and changelog are kept. Otherwise a new build is created.

Set symbols (or --symbols) to upload Breakpad symbols for the build.` + platformFlagHelp + "\n\n" + authHelp,
	Example: `  chauffeur addfiles --asset addfiles.yml
  chauffeur addfiles --asset addfiles.yml --os linux --arch arm64 --channel beta
  chauffeur addfiles --asset addfiles.yml --symbols build/game.sym --json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		assetFile, _ := cmd.Flags().GetString("asset")
		config, err := ParseYAML(assetFile)
		if err != nil {
			return err
		}
		if config.Spec != "addfiles" {
			return usageErrorf("%s has spec %q; chauffeur addfiles needs spec: addfiles", assetFile, config.Spec)
		}
		a := config.FilesAsset
		if v, _ := cmd.Flags().GetString("os"); v != "" {
			a.OS = v
		}
		if v, _ := cmd.Flags().GetString("arch"); v != "" {
			a.Arch = v
		}
		if v, _ := cmd.Flags().GetString("channel"); v != "" {
			a.Channel = v
		}
		if v, _ := cmd.Flags().GetString("engine-version"); v != "" {
			a.EngineVersion = v
		}
		if v, _ := cmd.Flags().GetString("symbols"); v != "" {
			a.Symbols = v
		}
		if a.App, err = appFromCmd(cmd, a.App); err != nil {
			return err
		}
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		return ProcessFiles(client, config)
	},
}

var genbuildCmd = &cobra.Command{
	Use:   "genbuild",
	Short: "Write a starter build.yml",
	Long: `Write build.yml in the current directory with starter values. --images adds
the images in a directory to media.gallery.`,
	Example: "  chauffeur genbuild --version 1.0.0 --engine-version 4.3 --images art/screenshots",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		imagesDir, _ := cmd.Flags().GetString("images")
		ver, _ := cmd.Flags().GetString("version")
		engine, _ := cmd.Flags().GetString("engine-version")
		if ver == "" {
			ver = "0.0.1"
		}
		engine, err := validateEngineVersion(engine)
		if err != nil {
			return err
		}
		var media *MediaSpec
		if imagesDir != "" {
			images, err := ScanImageDirectory(imagesDir)
			if err != nil {
				return usageErrorf("error scanning images directory: %v", err)
			}
			if len(images) > maxGalleryImages {
				return usageErrorf("%s has %d images; the gallery holds at most %d", imagesDir, len(images), maxGalleryImages)
			}
			media = &MediaSpec{Gallery: images}
			logf("Found %d image(s) in directory\n", len(images))
		}
		app, err := appFromCmd(cmd, nil)
		if err != nil {
			return err
		}
		if err := validateApp(app); err != nil {
			return err
		}
		config := &Config{
			Version: "v1",
			Spec:    "build",
			Asset: BuildAsset{
				Title:         "Untitled Build",
				Type:          "game",
				Description:   "No description provided",
				Version:       ver,
				EngineVersion: engine,
				App:           app,
				Media:         media,
				Changelog:     []ChangelogEntry{},
			},
		}
		if err := WriteYAMLFile("build.yml", config); err != nil {
			return usageErrorf("error writing build.yml: %v", err)
		}
		logln("Successfully generated build.yml")
		return nil
	},
}

var addchangelogCmd = &cobra.Command{
	Use:     "addchangelog --title T --description D",
	Short:   "Append a changelog entry to build.yml",
	Long:    "Append a changelog entry to build.yml in the current directory (at most 100 entries).",
	Example: `  chauffeur addchangelog --title "Fixed saves" --description "Saves no longer corrupt on exit."`,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		title, _ := cmd.Flags().GetString("title")
		description, _ := cmd.Flags().GetString("description")
		title, description = strings.TrimSpace(title), strings.TrimSpace(description)
		if title == "" || description == "" {
			return usageErrorf("--title and --description are required")
		}
		config, err := ReadYAMLFile("build.yml")
		if err != nil {
			return usageErrorf("error reading build.yml: %v", err)
		}
		if config.Spec != "build" {
			return usageErrorf("build.yml has spec %q, expected build", config.Spec)
		}
		assetData, err := yaml.Marshal(config.Asset)
		if err != nil {
			return err
		}
		var buildAsset BuildAsset
		if err := yaml.Unmarshal(assetData, &buildAsset); err != nil {
			return usageErrorf("failed to parse build asset: %v", err)
		}
		if len(buildAsset.Changelog) >= maxChangelogItems {
			return usageErrorf("build.yml already has %d changelog entries (the limit)", maxChangelogItems)
		}
		buildAsset.Changelog = append(buildAsset.Changelog, ChangelogEntry{Title: title, Description: description})
		config.Asset = buildAsset
		if err := WriteYAMLFile("build.yml", config); err != nil {
			return usageErrorf("error writing build.yml: %v", err)
		}
		logln("Successfully added changelog entry to build.yml")
		return nil
	},
}

var setfilesCmd = &cobra.Command{
	Use:   "setfiles",
	Short: "Write a starter addfiles.yml",
	Long:  "Write addfiles.yml in the current directory. --files adds every file in a directory." + platformFlagHelp,
	Example: `  chauffeur setfiles --files build/windows --os windows --arch x86_64 --version 1.0.0
  chauffeur setfiles --files build/linux --os linux --symbols build/symbols`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ver, _ := cmd.Flags().GetString("version")
		channel, _ := cmd.Flags().GetString("channel")
		osType, _ := cmd.Flags().GetString("os")
		assetType, _ := cmd.Flags().GetString("type")
		arch, _ := cmd.Flags().GetString("arch")
		filesDir, _ := cmd.Flags().GetString("files")
		engine, _ := cmd.Flags().GetString("engine-version")
		symbols, _ := cmd.Flags().GetString("symbols")
		if ver == "" {
			ver = "0.0.1"
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
		p, err := validatePlatform(osType, arch, channel)
		if err != nil {
			return err
		}
		engine, err = validateEngineVersion(engine)
		if err != nil {
			return err
		}
		var fileEntries []FileEntry
		if filesDir != "" {
			files, err := ScanFileDirectory(filesDir)
			if err != nil {
				return usageErrorf("error scanning files directory: %v", err)
			}
			for _, file := range files {
				fileEntries = append(fileEntries, FileEntry{File: file})
			}
			logf("Found %d file(s) in directory\n", len(fileEntries))
		}
		app, err := appFromCmd(cmd, nil)
		if err != nil {
			return err
		}
		if err := validateApp(app); err != nil {
			return err
		}
		config := &Config{
			Version: "v1",
			Spec:    "addfiles",
			Asset: FilesAsset{
				Type:          assetType,
				Version:       ver,
				EngineVersion: engine,
				App:           app,
				Channel:       p.Channel,
				OS:            p.OS,
				Arch:          p.Arch,
				Symbols:       symbols,
				Files:         fileEntries,
			},
		}
		if err := WriteYAMLFile("addfiles.yml", config); err != nil {
			return usageErrorf("error writing addfiles.yml: %v", err)
		}
		logln("Successfully generated addfiles.yml")
		return nil
	},
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.Version = version

	pf := rootCmd.PersistentFlags()
	pf.StringVarP(&accessToken, "access", "", "", "Deploy key access token (overrides BLAZIUM_ACCESS_TOKEN)")
	pf.StringVarP(&secretKey, "secret", "", "", "Deploy key secret (visible to other processes; prefer BLAZIUM_SECRET_KEY or --secret-stdin)")
	pf.Bool("secret-stdin", false, "Read the deploy key secret from the first line of stdin")
	pf.StringVarP(&apiURL, "url", "", defaultAPIURL, "API base URL (overrides BLAZIUM_API_URL)")
	pf.String("upload", defaultUploadURL, "Upload service base URL (overrides BLAZIUM_UPLOAD_URL)")
	pf.BoolVar(&jsonOutput, "json", false, "Print the result as one JSON object on stdout; progress goes to stderr")

	viper.BindPFlag("access", pf.Lookup("access"))
	viper.BindPFlag("secret", pf.Lookup("secret"))
	viper.BindPFlag("url", pf.Lookup("url"))
	viper.BindPFlag("upload", pf.Lookup("upload"))

	osUsage := "OS: " + osHelp
	archUsage := "Arch: " + archHelp
	channelUsage := "Channel: " + channelHelp
	engineUsage := "Blazium/Godot engine version, like 4.3 or 4.3.0-beta.2"
	symbolsUsage := "Breakpad symbols to upload for the build: a .sym file, a directory of .sym files, or a .zip"

	buildCmd.Flags().StringVarP(&assetFile, "asset", "", "", "Path to build.yml (required)")
	buildCmd.Flags().String("os", "", osUsage+". Replaces the file's platforms")
	buildCmd.Flags().String("arch", "", archUsage+" (default x86_64)")
	buildCmd.Flags().String("channel", "", channelUsage)
	buildCmd.Flags().String("engine-version", "", engineUsage)
	buildCmd.Flags().String("symbols", "", symbolsUsage+" (needs a single platform)")
	addAppFlags(buildCmd)

	addfilesCmd.Flags().StringVarP(&assetFile, "asset", "", "", "Path to addfiles.yml (required)")
	addfilesCmd.Flags().String("os", "", osUsage)
	addfilesCmd.Flags().String("arch", "", archUsage)
	addfilesCmd.Flags().String("channel", "", channelUsage)
	addfilesCmd.Flags().String("engine-version", "", engineUsage)
	addfilesCmd.Flags().String("symbols", "", symbolsUsage)
	addAppFlags(addfilesCmd)

	genbuildCmd.Flags().String("images", "", "Directory of gallery images to add (PNG, JPEG, GIF, WebP)")
	genbuildCmd.Flags().String("version", "", "Build version (default 0.0.1, at most 32 characters)")
	genbuildCmd.Flags().String("engine-version", "", engineUsage)
	addAppFlags(genbuildCmd)

	addchangelogCmd.Flags().String("title", "", "Changelog entry title (required)")
	addchangelogCmd.Flags().String("description", "", "Changelog entry description (required)")

	setfilesCmd.Flags().String("version", "", "Version (default 0.0.1)")
	setfilesCmd.Flags().String("channel", "", channelUsage+" (default stable)")
	setfilesCmd.Flags().String("os", "", osUsage+" (default windows)")
	setfilesCmd.Flags().String("type", "", "Asset type (default game)")
	setfilesCmd.Flags().String("arch", "", archUsage+" (default x86_64)")
	setfilesCmd.Flags().String("files", "", "Directory of files to add")
	setfilesCmd.Flags().String("engine-version", "", engineUsage)
	setfilesCmd.Flags().String("symbols", "", symbolsUsage)
	addAppFlags(setfilesCmd)

	buildCmd.MarkFlagRequired("asset")
	addfilesCmd.MarkFlagRequired("asset")
	addchangelogCmd.MarkFlagRequired("title")
	addchangelogCmd.MarkFlagRequired("description")

	lobbyCmd.AddCommand(lobbyPublishCmd, lobbyListCmd, lobbyStatusCmd)
	rootCmd.AddCommand(buildCmd, addfilesCmd, symbolsCmd, mediaCmd, infoCmd, buildsCmd,
		genbuildCmd, addchangelogCmd, setfilesCmd, gendocsCmd, lobbyCmd)
}

// initConfig reads in environment variables and config file if set
func initConfig() {
	viper.SetEnvPrefix("BLAZIUM")
	viper.BindEnv("access", "BLAZIUM_ACCESS_TOKEN")
	viper.BindEnv("secret", "BLAZIUM_SECRET_KEY")
	viper.BindEnv("url", "BLAZIUM_API_URL")
	viper.BindEnv("upload", "BLAZIUM_UPLOAD_URL")
	viper.SetDefault("url", defaultAPIURL)
	viper.SetDefault("upload", defaultUploadURL)
	viper.AutomaticEnv()
}

// deployClient builds an authenticated client from flags and environment.
func deployClient(cmd *cobra.Command) (*Client, error) {
	token := strings.TrimSpace(viper.GetString("access"))
	secret, err := resolveSecret(cmd)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, usageErrorf("the deploy key access token is missing: set BLAZIUM_ACCESS_TOKEN (or --access)")
	}
	if secret == "" {
		return nil, usageErrorf("the deploy key secret is missing: set BLAZIUM_SECRET_KEY or pipe it with --secret-stdin")
	}
	for _, u := range []string{viper.GetString("url"), viper.GetString("upload")} {
		if err := checkServiceURL(u); err != nil {
			return nil, err
		}
	}
	client := NewClient(viper.GetString("url"), token, secret)
	client.SetUploadURL(viper.GetString("upload"))
	return client, nil
}

// resolveSecret reads the secret from stdin with --secret-stdin, otherwise from
// --secret or BLAZIUM_SECRET_KEY.
func resolveSecret(cmd *cobra.Command) (string, error) {
	if fromStdin, _ := cmd.Flags().GetBool("secret-stdin"); fromStdin {
		return readSecret(os.Stdin)
	}
	if f := cmd.Flags().Lookup("secret"); f != nil && f.Changed {
		fmt.Fprintln(stderr, "Warning: --secret is visible in the process list and shell history. Use BLAZIUM_SECRET_KEY or --secret-stdin.")
	}
	return strings.TrimSpace(viper.GetString("secret")), nil
}

func readSecret(r io.Reader) (string, error) {
	line, err := bufio.NewReader(io.LimitReader(r, 4096)).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("failed to read secret from stdin: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func main() {
	err := rootCmd.Execute()
	if err == nil {
		return
	}
	if jsonOutput {
		emitError(err)
	} else {
		fmt.Fprintf(stderr, "Error: %v\n", err)
	}
	os.Exit(exitCode(err))
}
