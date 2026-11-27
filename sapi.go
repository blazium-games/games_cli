package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const sapiDevBinaryURL = "https://cdn.blazium.online/tools/games_sapi/linux-amd64/games_sapi"

var (
	sapiChannelRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	sapiChecksumRe = regexp.MustCompile(`^[a-f0-9]{64}$`)
	sapiNameRe     = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	sapiLogicRe    = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
)

// sapiPlanCaps mirrors the pack limits games_upload enforces.
var sapiPlanCaps = map[string]struct {
	PackBytes int64
	Files     int
}{
	"free":     {256 << 10, 20},
	"premium":  {2 << 20, 100},
	"business": {8 << 20, 250},
}

var (
	sapiDir       string
	sapiChannel   string
	sapiActivate  bool
	sapiPlan      string
	sapiBinary    string
	sapiPort      int
	sapiUsers     string
	sapiLogLogic  string
	sapiLogLevel  string
	sapiLogSince  string
	sapiLogUntil  string
	sapiLogCursor string
	sapiLogReq    string
	sapiLogLimit  int
	sapiPublic    bool
	sapiFromFile  string
	sapiScope     string
	sapiUser      string
	sapiPrefix    string
	sapiLimit     int
	sapiTTL       int64
	sapiOut       string
	sapiToChannel string

	sapiResetScope   string
	sapiResetChannel string
	sapiResetUser    string
	sapiResetPrefix  string
	sapiResetConfirm string
	sapiResetDry     bool
)

var sapiCmd = &cobra.Command{
	Use:   "sapi",
	Short: "Publish and manage Scripted API logic for this game",
}

var sapiPublishCmd = &cobra.Command{
	Use:   "publish --dir <path>",
	Short: "Pack every .lua and .luau file and upload it as a Scripted API pack",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		return runSapiPublish(client, sapiDir, sapiChannel, sapiActivate)
	},
}

var sapiValidateCmd = &cobra.Command{
	Use:   "validate --dir <path>",
	Short: "Check a pack against a plan's limits before publishing",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := runSapiValidate(sapiDir, sapiPlan, sapiBinary); err != nil {
			return err
		}
		// With a deploy key to hand, say which logic files belong to features that are off. Without one, say nothing.
		if client, err := deployClient(cmd); err == nil {
			if files, ferr := lobbyLuaFiles(sapiDir); ferr == nil {
				warnDisabledFeatures(client, files)
			}
		}
		return nil
	},
}

var sapiTestCmd = &cobra.Command{
	Use:   "test --dir <path>",
	Short: "Run the pack's *.test.luau files with the local emulator",
	RunE: func(cmd *cobra.Command, args []string) error {
		bin, err := findSapiBinary(sapiBinary, false)
		if err != nil {
			return err
		}
		plan, err := sapiPlanName(sapiPlan)
		if err != nil {
			return err
		}
		return runSapiBinary(bin, []string{"--test", sapiDir, "--plan", plan}, nil)
	},
}

var sapiDevCmd = &cobra.Command{
	Use:   "dev --dir <path>",
	Short: "Serve the pack locally with the Scripted API emulator",
	RunE: func(cmd *cobra.Command, args []string) error {
		bin, err := findSapiBinary(sapiBinary, true)
		if err != nil {
			return err
		}
		features, err := sapiDevFeatures(sapiDevAll, sapiDevList)
		if err != nil {
			return err
		}
		env, err := sapiDevEnv(sapiDir, sapiPlan, sapiPort, sapiUsers, features)
		if err != nil {
			return err
		}
		if features != "" {
			logf("Emulating a game with these features on: %s\n", features)
		}
		logf("Scripted API emulator on http://127.0.0.1:%d/v1/dev/<logic> (Ctrl+C to stop)\n", sapiPort)
		return runSapiBinary(bin, nil, env)
	},
}

var sapiStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show settings, active packs per channel and the plan's limits",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sapiGet(cmd, "/tool/sapi", printSapiStatus)
	},
}

var sapiPacksCmd = &cobra.Command{
	Use:   "packs",
	Short: "List stored Scripted API packs",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sapiGet(cmd, "/tool/sapi/packs", printSapiPacks)
	},
}

var sapiActivateCmd = &cobra.Command{
	Use:   "activate <checksum>",
	Short: "Serve a stored pack on a channel (pass \"none\" to stop the channel)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		body, err := sapiActivateBody(args[0], sapiChannel)
		if err != nil {
			return err
		}
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		resp, err := client.PostJSON("/tool/sapi/activate", body)
		if err != nil {
			return err
		}
		if jsonOutput {
			return emit(resp.Data)
		}
		if body["checksum"] == "" {
			logf("Stopped channel %s\n", body["channel"])
		} else {
			logf("Channel %s now serves %s\n", body["channel"], shortChecksum(body["checksum"]))
		}
		return nil
	},
}

var sapiLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Read recent script logs",
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := sapiLogsPath(sapiLogLogic, sapiChannel, sapiLogLevel, sapiLogReq, sapiLogSince, sapiLogUntil, sapiLogCursor, sapiLogLimit)
		if err != nil {
			return err
		}
		return sapiGet(cmd, path, printSapiLogs)
	},
}

var sapiUsageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Show stored data against the plan's limits",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sapiGet(cmd, "/tool/sapi/usage", printSapiUsage)
	},
}

var sapiConfigCmd = &cobra.Command{Use: "config", Short: "Read and change remote config values"}

var sapiConfigGetCmd = &cobra.Command{
	Use:   "get",
	Short: "List remote config values",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sapiGet(cmd, "/tool/sapi/config", printSapiItems)
	},
}

var sapiConfigSetCmd = &cobra.Command{
	Use:   "set <name> <json value>",
	Short: "Set a remote config value (plain text is stored as a string)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		body, err := sapiConfigBody(args[0], args[1], sapiChannel, sapiPublic)
		if err != nil {
			return err
		}
		return sapiSend(cmd, "PUT", "/tool/sapi/config", body, "Saved config "+args[0])
	},
}

var sapiConfigDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a remote config value",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := sapiNamedPath("/tool/sapi/config", args[0], sapiChannel)
		if err != nil {
			return err
		}
		return sapiSend(cmd, "DELETE", path, nil, "Deleted config "+args[0])
	},
}

var sapiSecretCmd = &cobra.Command{Use: "secret", Short: "Store write-only secrets for scripts"}

var sapiSecretListCmd = &cobra.Command{
	Use:   "list",
	Short: "List secret names (values are never shown)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sapiGet(cmd, "/tool/sapi/secrets", printSapiItems)
	},
}

var sapiSecretSetCmd = &cobra.Command{
	Use:   "set <name>",
	Short: "Store a secret; the value is read from stdin or --from-file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !sapiNameRe.MatchString(args[0]) {
			return usageErrorf("secret names use letters, digits, _ - . and at most 64 characters")
		}
		if fromStdin, _ := cmd.Flags().GetBool("secret-stdin"); fromStdin && sapiFromFile == "" {
			return usageErrorf("the secret value is read from stdin, so set BLAZIUM_SECRET_KEY or pass --from-file")
		}
		value, err := readSapiSecretValue(sapiFromFile, os.Stdin)
		if err != nil {
			return err
		}
		channel, err := sapiOptionalChannel(sapiChannel)
		if err != nil {
			return err
		}
		return sapiSend(cmd, "PUT", "/tool/sapi/secrets", map[string]any{"channel": channel, "name": args[0], "value": value}, "Stored secret "+args[0])
	},
}

var sapiSecretDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a secret",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := sapiNamedPath("/tool/sapi/secrets", args[0], sapiChannel)
		if err != nil {
			return err
		}
		return sapiSend(cmd, "DELETE", path, nil, "Deleted secret "+args[0])
	},
}

var sapiSchedulesCmd = &cobra.Command{
	Use:   "schedules",
	Short: "List scheduled logic and recent runs",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sapiGet(cmd, "/tool/sapi/jobs", printSapiJobs)
	},
}

var sapiSchedulesRunCmd = &cobra.Command{
	Use:   "run <logic>",
	Short: "Run a scheduled logic now",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !sapiLogicRe.MatchString(args[0]) {
			return usageErrorf("logic names are lowercase letters, digits and _")
		}
		return sapiSend(cmd, "POST", "/tool/sapi/jobs/"+args[0]+"/run", map[string]any{}, "Queued "+args[0])
	},
}

var sapiDataCmd = &cobra.Command{Use: "data", Short: "Read and change stored script data"}

var sapiDataListCmd = &cobra.Command{
	Use:   "list",
	Short: "List keys in a scope",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := sapiSliceFlags()
		if err != nil {
			return err
		}
		return sapiGet(cmd, s.listPath(sapiPrefix, "", sapiLimit), printSapiData)
	},
}

var sapiDataGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Print one stored value",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := sapiSliceFlags()
		if err != nil {
			return err
		}
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		resp, err := client.GetJSON(s.listPath(args[0], "", 200))
		if err != nil {
			return err
		}
		for _, item := range sapiItems(resp.Data) {
			if item["key"] == args[0] {
				if jsonOutput {
					return emit(item)
				}
				raw, _ := json.MarshalIndent(item["value"], "", "  ")
				fmt.Fprintln(stdout, termText(string(raw)))
				return nil
			}
		}
		return &apiError{Status: 404, Code: 4040, Message: "no value is stored under " + args[0]}
	},
}

var sapiDataSetCmd = &cobra.Command{
	Use:   "set <key> <json value>",
	Short: "Store a value (plain text is stored as a string)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := sapiSliceFlags()
		if err != nil {
			return err
		}
		body := map[string]any{"key": args[0], "value": parseSapiValue(args[1])}
		if sapiTTL > 0 {
			body["ttl"] = sapiTTL
		}
		return sapiSend(cmd, "PUT", s.path("/tool/sapi/data", nil), body, "Stored "+args[0])
	},
}

var sapiDataDeleteCmd = &cobra.Command{
	Use:   "delete <key>",
	Short: "Delete one stored value",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := sapiSliceFlags()
		if err != nil {
			return err
		}
		return sapiSend(cmd, "DELETE", s.path("/tool/sapi/data/key", url.Values{"key": {args[0]}}), nil, "Deleted "+args[0])
	},
}

var sapiDataExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Write every key in a scope as JSON (to --out or stdout)",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := sapiSliceFlags()
		if err != nil {
			return err
		}
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		items, err := exportSapiSlice(client, s, sapiPrefix)
		if err != nil {
			return err
		}
		raw, _ := json.MarshalIndent(items, "", "  ")
		if sapiOut == "" {
			fmt.Fprintln(stdout, termText(string(raw)))
			return nil
		}
		if err := os.WriteFile(sapiOut, append(raw, '\n'), 0o600); err != nil {
			return err
		}
		logf("Wrote %d keys to %s\n", len(items), sapiOut)
		return nil
	},
}

var sapiDataImportCmd = &cobra.Command{
	Use:   "import <file>",
	Short: "Store every {key, value} in a JSON file made by export",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := sapiSliceFlags()
		if err != nil {
			return err
		}
		items, err := readSapiExport(args[0])
		if err != nil {
			return err
		}
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		n, err := importSapiItems(client, s, items)
		if err != nil {
			return fmt.Errorf("stored %d of %d keys: %w", n, len(items), err)
		}
		if jsonOutput {
			return emit(map[string]any{"ok": true, "stored": n})
		}
		logf("Stored %d keys\n", n)
		return nil
	},
}

var sapiDataResetCmd = &cobra.Command{
	Use:   "reset --scope global|channel|user --confirm <game id>",
	Short: "Delete many stored values at once (use --dry-run to count first)",
	Long: "Deletes every key in a scope, or only the keys that start with --prefix. With --scope channel and no --channel it covers every channel; " +
		"with --scope user and no --user it covers every player. --dry-run only counts what would be removed. " +
		"--confirm must be this game's id. The whole-game purge is not available here; use the dashboard or MCP.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := sapiResetPath(sapiResetScope, sapiResetChannel, sapiResetUser, sapiResetPrefix, sapiResetConfirm, sapiResetDry)
		if err != nil {
			return err
		}
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		resp, err := client.Delete(path)
		if err != nil {
			return err
		}
		if jsonOutput {
			return emit(resp.Data)
		}
		printSapiReset(resp.Data)
		return nil
	},
}

var sapiDataCopyCmd = &cobra.Command{
	Use:   "copy --channel <from> --to <to>",
	Short: "Copy every key in one channel's data into another channel",
	RunE: func(cmd *cobra.Command, args []string) error {
		from, err := sapiOptionalChannel(sapiChannel)
		if err != nil || from == "" {
			return usageErrorf("--channel names the channel to copy from")
		}
		to, err := sapiOptionalChannel(sapiToChannel)
		if err != nil || to == "" || to == from {
			return usageErrorf("--to names a different channel to copy into")
		}
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		items, err := exportSapiSlice(client, sapiSlice{scope: "channel", channel: from}, sapiPrefix)
		if err != nil {
			return err
		}
		n, err := importSapiItems(client, sapiSlice{scope: "channel", channel: to}, items)
		if err != nil {
			return fmt.Errorf("copied %d of %d keys: %w", n, len(items), err)
		}
		if jsonOutput {
			return emit(map[string]any{"ok": true, "copied": n})
		}
		logf("Copied %d keys from %s to %s\n", n, from, to)
		return nil
	},
}

func init() {
	for _, c := range []*cobra.Command{sapiPublishCmd, sapiValidateCmd, sapiTestCmd, sapiDevCmd} {
		c.Flags().StringVar(&sapiDir, "dir", "", "Directory of .lua and .luau files")
		_ = c.MarkFlagRequired("dir")
	}
	for _, c := range []*cobra.Command{sapiValidateCmd, sapiTestCmd, sapiDevCmd} {
		c.Flags().StringVar(&sapiPlan, "plan", "free", "Plan limits to apply: free, premium or business")
		c.Flags().StringVar(&sapiBinary, "binary", "", "Path to the games_sapi binary (default: SAPI_BINARY, then PATH)")
	}
	sapiPublishCmd.Flags().StringVar(&sapiChannel, "channel", "stable", "Release channel the pack is for")
	sapiPublishCmd.Flags().BoolVar(&sapiActivate, "activate", false, "Serve the pack on the channel once it validates")
	sapiActivateCmd.Flags().StringVar(&sapiChannel, "channel", "stable", "Release channel")
	sapiDevCmd.Flags().IntVar(&sapiPort, "port", 8787, "Local port")
	sapiDevCmd.Flags().StringVar(&sapiUsers, "users", "", "JSON file of fake users for the emulator")

	sapiLogsCmd.Flags().StringVar(&sapiLogLogic, "logic", "", "Only this logic")
	sapiLogsCmd.Flags().StringVar(&sapiChannel, "channel", "", "Only this channel")
	sapiLogsCmd.Flags().StringVar(&sapiLogLevel, "level", "", "debug, info, warn or error")
	sapiLogsCmd.Flags().StringVar(&sapiLogSince, "since", "", "Only lines after this: a time (RFC 3339 or Unix ms) or how long ago (30m, 2h, 7d)")
	sapiLogsCmd.Flags().StringVar(&sapiLogUntil, "until", "", "Only lines up to this: a time (RFC 3339 or Unix ms) or how long ago (30m, 2h, 7d)")
	sapiLogsCmd.Flags().StringVar(&sapiLogCursor, "cursor", "", "Continue from the next_cursor a full page printed (older lines)")
	sapiLogsCmd.Flags().StringVar(&sapiLogReq, "request-id", "", "Only lines for one request")
	sapiLogsCmd.Flags().IntVar(&sapiLogLimit, "limit", 200, "Most lines to return")

	for _, c := range []*cobra.Command{sapiConfigSetCmd, sapiConfigDeleteCmd, sapiSecretSetCmd, sapiSecretDeleteCmd} {
		c.Flags().StringVar(&sapiChannel, "channel", "", "Release channel (empty applies to every channel)")
	}
	sapiConfigSetCmd.Flags().BoolVar(&sapiPublic, "public", false, "Let game clients read this value")
	sapiSecretSetCmd.Flags().StringVar(&sapiFromFile, "from-file", "", "Read the value from this file instead of stdin")

	for _, c := range []*cobra.Command{sapiDataListCmd, sapiDataGetCmd, sapiDataSetCmd, sapiDataDeleteCmd, sapiDataExportCmd, sapiDataImportCmd} {
		c.Flags().StringVar(&sapiScope, "scope", "global", "global, channel or user")
		c.Flags().StringVar(&sapiChannel, "channel", "", "Release channel for the channel scope (default stable)")
		c.Flags().StringVar(&sapiUser, "user", "", "User id for the user scope")
	}
	sapiDataListCmd.Flags().StringVar(&sapiPrefix, "prefix", "", "Only keys starting with this")
	sapiDataListCmd.Flags().IntVar(&sapiLimit, "limit", 50, "Most keys to return")
	sapiDataExportCmd.Flags().StringVar(&sapiPrefix, "prefix", "", "Only keys starting with this")
	sapiDataExportCmd.Flags().StringVar(&sapiOut, "out", "", "File to write (default stdout)")
	sapiDataSetCmd.Flags().Int64Var(&sapiTTL, "ttl", 0, "Seconds until the value expires (0 keeps it)")
	sapiDataCopyCmd.Flags().StringVar(&sapiChannel, "channel", "", "Channel to copy from")
	sapiDataCopyCmd.Flags().StringVar(&sapiToChannel, "to", "", "Channel to copy into")
	sapiDataCopyCmd.Flags().StringVar(&sapiPrefix, "prefix", "", "Only keys starting with this")

	sapiDataResetCmd.Flags().StringVar(&sapiResetScope, "scope", "", "global, channel or user (required)")
	sapiDataResetCmd.Flags().StringVar(&sapiResetChannel, "channel", "", "Only this release channel (channel scope)")
	sapiDataResetCmd.Flags().StringVar(&sapiResetUser, "user", "", "Only this player (user scope)")
	sapiDataResetCmd.Flags().StringVar(&sapiResetPrefix, "prefix", "", "Only keys starting with this")
	sapiDataResetCmd.Flags().StringVar(&sapiResetConfirm, "confirm", "", "This game's id (required)")
	sapiDataResetCmd.Flags().BoolVar(&sapiResetDry, "dry-run", false, "Count what would be removed and change nothing")

	sapiConfigCmd.AddCommand(sapiConfigGetCmd, sapiConfigSetCmd, sapiConfigDeleteCmd)
	sapiSecretCmd.AddCommand(sapiSecretListCmd, sapiSecretSetCmd, sapiSecretDeleteCmd)
	sapiSchedulesCmd.AddCommand(sapiSchedulesRunCmd)
	sapiDataCmd.AddCommand(sapiDataListCmd, sapiDataGetCmd, sapiDataSetCmd, sapiDataDeleteCmd, sapiDataExportCmd, sapiDataImportCmd, sapiDataCopyCmd, sapiDataResetCmd)
	sapiCmd.AddCommand(sapiPublishCmd, sapiValidateCmd, sapiTestCmd, sapiDevCmd, sapiStatusCmd, sapiPacksCmd, sapiActivateCmd,
		sapiLogsCmd, sapiUsageCmd, sapiConfigCmd, sapiSecretCmd, sapiSchedulesCmd, sapiDataCmd)
}

func runSapiPublish(client *Client, dir, channel string, activate bool) error {
	channel, err := sapiOptionalChannel(channel)
	if err != nil {
		return err
	}
	if channel == "" {
		channel = "stable"
	}
	files, err := lobbyLuaFiles(dir)
	if err != nil {
		return err
	}
	blob, err := storedZip(files)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(blob)
	checksum := hex.EncodeToString(sum[:])
	tmp, err := os.CreateTemp("", "sapi-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(blob); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	resp, _, err := client.PostMultipart(client.filesURL("/tool/sapi/publish"), []formField{
		{"checksum", checksum},
		{"channel", channel},
		{"activate", strconv.FormatBool(activate)},
	}, []formFile{{Field: "file", Path: tmp.Name(), Length: -1}}, nil)
	if err != nil {
		return err
	}
	warnDisabledFeatures(client, files)
	if jsonOutput {
		return emit(map[string]any{"ok": true, "checksum": checksum, "data": resp.Data})
	}
	logf("Published Scripted API pack %s for %s (%d files)\n", shortChecksum(checksum), channel, len(files))
	printSapiDiagnostics(resp.Data["diagnostics"])
	switch {
	case resp.Data["load_error"] != nil && resp.Data["load_error"] != "":
		logf("Stored, but not checked: %v\n", resp.Data["load_error"])
	case resp.Data["valid"] == false:
		logf("The pack did not validate, so it was not activated\n")
	case resp.Data["activated"] == true:
		logf("Channel %s now serves this pack\n", channel)
	case activate:
		logf("Not activated\n")
	default:
		logf("Activate it with: chauffeur sapi activate %s --channel %s\n", checksum, channel)
	}
	return nil
}

func printSapiDiagnostics(v any) {
	list, _ := v.([]any)
	for _, d := range list {
		m, _ := d.(map[string]any)
		if m == nil {
			continue
		}
		line := ""
		if n, ok := m["line"].(float64); ok && n > 0 {
			line = fmt.Sprintf(":%d", int(n))
		}
		logf("  %v%s: %v\n", m["file"], line, m["message"])
	}
}

func sapiPlanName(plan string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(plan))
	switch p {
	case "", "free":
		return "free", nil
	case "premium", "grow":
		return "premium", nil
	case "business", "scale":
		return "business", nil
	}
	return "", usageErrorf("--plan must be free, premium or business")
}

// checkSapiPack applies the upload service's size and file caps locally.
func checkSapiPack(files []lobbyFile, plan string) (int64, error) {
	caps := sapiPlanCaps[plan]
	if len(files) > caps.Files {
		return 0, usageErrorf("the pack has %d Luau files; the %s plan allows %d", len(files), plan, caps.Files)
	}
	var total int64
	for _, f := range files {
		total += int64(len(f.body))
	}
	if total > caps.PackBytes {
		return total, usageErrorf("the pack is %d bytes; the %s plan allows %d", total, plan, caps.PackBytes)
	}
	return total, nil
}

func runSapiValidate(dir, plan, binary string) error {
	plan, err := sapiPlanName(plan)
	if err != nil {
		return err
	}
	files, err := lobbyLuaFiles(dir)
	if err != nil {
		return err
	}
	total, err := checkSapiPack(files, plan)
	if err != nil {
		return err
	}
	bin, findErr := findSapiBinary(binary, false)
	if findErr == nil {
		return runSapiBinary(bin, []string{"--validate", dir, "--plan", plan}, nil)
	}
	if strings.TrimSpace(binary) != "" || strings.TrimSpace(os.Getenv("SAPI_BINARY")) != "" {
		return findErr
	}
	if jsonOutput {
		return emit(map[string]any{"ok": true, "plan": plan, "files": len(files), "bytes": total, "compiled": false})
	}
	logf("ok: %d Luau files, %d bytes, within the %s plan\n", len(files), total, plan)
	logf("Luau was not compiled: pass --binary or set SAPI_BINARY to the games_sapi binary for a full check\n")
	return nil
}

// findSapiBinary looks for the games_sapi binary in --binary, SAPI_BINARY,
// PATH and the chauffeur cache. download fetches it when none is found.
func findSapiBinary(flag string, download bool) (string, error) {
	for _, candidate := range []string{flag, os.Getenv("SAPI_BINARY")} {
		if candidate = strings.TrimSpace(candidate); candidate != "" {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
			return "", usageErrorf("games_sapi binary not found at %s", candidate)
		}
	}
	if p, err := exec.LookPath("games_sapi"); err == nil {
		return p, nil
	}
	cached := sapiCachedBinary()
	if cached != "" {
		if info, err := os.Stat(cached); err == nil && !info.IsDir() {
			return cached, nil
		}
	}
	if !download {
		return "", usageErrorf("the games_sapi binary was not found: pass --binary or set SAPI_BINARY")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || cached == "" {
		return "", usageErrorf("the games_sapi emulator is built for Linux amd64: run chauffeur in WSL or a Linux container, or pass --binary")
	}
	if err := downloadSapiBinary(cached); err != nil {
		return "", err
	}
	return cached, nil
}

func sapiCachedBinary() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "chauffeur", "games_sapi")
}

func downloadSapiBinary(dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	logf("Downloading the Scripted API emulator from %s\n", sapiDevBinaryURL)
	c := NewClient("", "", "")
	sumFile := dest + ".sha256"
	tmp := dest + ".download"
	defer os.Remove(sumFile)
	defer os.Remove(tmp)
	if err := c.DownloadFile(sapiDevBinaryURL+".sha256", sumFile); err != nil {
		return fmt.Errorf("downloading the emulator checksum: %w", err)
	}
	if err := c.DownloadFile(sapiDevBinaryURL, tmp); err != nil {
		return fmt.Errorf("downloading the emulator: %w", err)
	}
	want, err := os.ReadFile(sumFile)
	if err != nil {
		return err
	}
	fields := strings.Fields(string(want))
	got, err := fileSHA256(tmp)
	if err != nil {
		return err
	}
	if len(fields) == 0 || !strings.EqualFold(fields[0], got) {
		return fmt.Errorf("the downloaded emulator does not match its checksum")
	}
	if err := os.Chmod(tmp, 0o700); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func sapiDevEnv(dir, plan string, port int, users, features string) ([]string, error) {
	plan, err := sapiPlanName(plan)
	if err != nil {
		return nil, err
	}
	if port <= 0 || port > 65535 {
		return nil, usageErrorf("--port must be between 1 and 65535")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return nil, usageErrorf("--dir must be a directory")
	}
	env := []string{"SAPI_MODE=dev", "SAPI_BIND=127.0.0.1", "SAPI_PORT=" + strconv.Itoa(port), "SAPI_DEV_DIR=" + abs, "SAPI_DEV_PLAN=" + plan}
	if users != "" {
		u, err := filepath.Abs(users)
		if err != nil {
			return nil, err
		}
		env = append(env, "SAPI_DEV_USERS="+u)
	}
	if features != "" {
		env = append(env, "SAPI_DEV_FEATURES="+features)
	}
	return env, nil
}

func runSapiBinary(bin string, args, env []string) error {
	c := exec.Command(bin, args...)
	c.Stdout = stdout
	c.Stderr = stderr
	c.Stdin = os.Stdin
	c.Env = append(os.Environ(), env...)
	if err := c.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return usageErrorf("games_sapi exited with code %d", exit.ExitCode())
		}
		return err
	}
	return nil
}

func sapiOptionalChannel(channel string) (string, error) {
	ch := strings.ToLower(strings.TrimSpace(channel))
	if ch != "" && !sapiChannelRe.MatchString(ch) {
		return "", usageErrorf("channels are short lowercase names like stable or beta")
	}
	return ch, nil
}

func sapiActivateBody(checksum, channel string) (map[string]string, error) {
	ch, err := sapiOptionalChannel(channel)
	if err != nil {
		return nil, err
	}
	if ch == "" {
		ch = "stable"
	}
	sum := strings.ToLower(strings.TrimSpace(checksum))
	if sum == "none" {
		sum = ""
	} else if !sapiChecksumRe.MatchString(sum) {
		return nil, usageErrorf("the checksum is the 64-character value from chauffeur sapi packs")
	}
	return map[string]string{"checksum": sum, "channel": ch}, nil
}

var (
	sapiNow     = time.Now
	sapiAgoRe   = regexp.MustCompile(`^([1-9][0-9]{0,5})([smhd])$`)
	sapiCursorR = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
)

// sapiLogTime turns a flag into what the log API reads: Unix milliseconds or RFC 3339 pass through,
// and "15m", "2h" or "7d" become that long before now.
func sapiLogTime(flag, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if m := sapiAgoRe.FindStringSubmatch(value); m != nil {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		unit := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[2]]
		return strconv.FormatInt(sapiNow().Add(-time.Duration(n)*unit).UnixMilli(), 10), nil
	}
	if _, err := strconv.ParseInt(value, 10, 64); err == nil {
		return value, nil
	}
	if _, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return value, nil
	}
	return "", usageErrorf("--%s must be Unix milliseconds, an RFC 3339 time, or how long ago such as 30m, 2h or 7d", flag)
}

func sapiLogsPath(logic, channel, level, requestID, since, until, cursor string, limit int) (string, error) {
	q := url.Values{}
	if logic != "" {
		if !sapiLogicRe.MatchString(logic) {
			return "", usageErrorf("logic names are lowercase letters, digits and _")
		}
		q.Set("logic", logic)
	}
	ch, err := sapiOptionalChannel(channel)
	if err != nil {
		return "", err
	}
	if ch != "" {
		q.Set("channel", ch)
	}
	switch level {
	case "":
	case "debug", "info", "warn", "error":
		q.Set("level", level)
	default:
		return "", usageErrorf("--level must be debug, info, warn or error")
	}
	if requestID != "" {
		q.Set("request_id", requestID)
	}
	for _, t := range []struct{ flag, key, value string }{{"since", "since", since}, {"until", "until", until}} {
		v, err := sapiLogTime(t.flag, t.value)
		if err != nil {
			return "", err
		}
		if v != "" {
			q.Set(t.key, v)
		}
	}
	if cursor != "" {
		if !sapiCursorR.MatchString(cursor) {
			return "", usageErrorf("--cursor is the next_cursor a full page printed")
		}
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(min(limit, 1000)))
	}
	if len(q) == 0 {
		return "/tool/sapi/logs", nil
	}
	return "/tool/sapi/logs?" + q.Encode(), nil
}

// parseSapiValue reads JSON and keeps anything else as a string.
func parseSapiValue(raw string) any {
	var v any
	if json.Unmarshal([]byte(raw), &v) == nil {
		return v
	}
	return raw
}

func sapiConfigBody(name, value, channel string, public bool) (map[string]any, error) {
	if !sapiNameRe.MatchString(name) {
		return nil, usageErrorf("config names use letters, digits, _ - . and at most 64 characters")
	}
	ch, err := sapiOptionalChannel(channel)
	if err != nil {
		return nil, err
	}
	return map[string]any{"channel": ch, "name": name, "value": parseSapiValue(value), "public": public}, nil
}

func sapiNamedPath(base, name, channel string) (string, error) {
	if !sapiNameRe.MatchString(name) {
		return "", usageErrorf("names use letters, digits, _ - . and at most 64 characters")
	}
	ch, err := sapiOptionalChannel(channel)
	if err != nil {
		return "", err
	}
	q := url.Values{"name": {name}}
	if ch != "" {
		q.Set("channel", ch)
	}
	return base + "?" + q.Encode(), nil
}

func readSapiSecretValue(file string, stdin io.Reader) (string, error) {
	var raw []byte
	var err error
	if file != "" {
		raw, err = os.ReadFile(file)
	} else {
		raw, err = io.ReadAll(io.LimitReader(bufio.NewReader(stdin), 16<<10+1))
	}
	if err != nil {
		return "", err
	}
	if len(raw) > 16<<10 {
		return "", usageErrorf("secret values are at most 16 KiB")
	}
	value := strings.TrimRight(string(raw), "\r\n")
	if value == "" {
		return "", usageErrorf("the secret value is empty")
	}
	return value, nil
}

type sapiSlice struct {
	scope   string
	channel string
	user    string
}

func sapiSliceFlags() (sapiSlice, error) {
	return newSapiSlice(sapiScope, sapiChannel, sapiUser)
}

func newSapiSlice(scope, channel, user string) (sapiSlice, error) {
	s := sapiSlice{scope: strings.ToLower(strings.TrimSpace(scope)), user: strings.TrimSpace(user)}
	ch, err := sapiOptionalChannel(channel)
	if err != nil {
		return s, err
	}
	switch s.scope {
	case "global":
		if ch != "" || s.user != "" {
			return s, usageErrorf("the global scope takes no --channel or --user")
		}
	case "channel":
		if ch == "" {
			ch = "stable"
		}
		s.channel = ch
	case "user":
		if s.user == "" {
			return s, usageErrorf("the user scope needs --user")
		}
		s.channel = ch
	default:
		return s, usageErrorf("--scope must be global, channel or user")
	}
	return s, nil
}

func (s sapiSlice) path(base string, extra url.Values) string {
	q := url.Values{"scope": {s.scope}}
	if s.channel != "" {
		q.Set("channel", s.channel)
	}
	if s.user != "" {
		q.Set("user", s.user)
	}
	for k, v := range extra {
		q[k] = v
	}
	return base + "?" + q.Encode()
}

func (s sapiSlice) listPath(prefix, after string, limit int) string {
	extra := url.Values{}
	if prefix != "" {
		extra.Set("prefix", prefix)
	}
	if after != "" {
		extra.Set("after", after)
	}
	if limit > 0 {
		extra.Set("limit", strconv.Itoa(min(limit, 200)))
	}
	return s.path("/tool/sapi/data", extra)
}

func sapiItems(data map[string]any) []map[string]any {
	list, _ := data["items"].([]any)
	out := make([]map[string]any, 0, len(list))
	for _, v := range list {
		if m, ok := v.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

type sapiExportItem struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

const sapiExportMax = 100000

func exportSapiSlice(client *Client, s sapiSlice, prefix string) ([]sapiExportItem, error) {
	var out []sapiExportItem
	after := ""
	for {
		resp, err := client.GetJSON(s.listPath(prefix, after, 200))
		if err != nil {
			return out, err
		}
		for _, item := range sapiItems(resp.Data) {
			key, _ := item["key"].(string)
			out = append(out, sapiExportItem{Key: key, Value: item["value"]})
		}
		next, _ := resp.Data["next"].(string)
		if next == "" || next == after || len(out) >= sapiExportMax {
			return out, nil
		}
		after = next
	}
}

func readSapiExport(path string) ([]sapiExportItem, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var items []sapiExportItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, usageErrorf("%s is not a JSON list of {key, value}", path)
	}
	for i, item := range items {
		if item.Key == "" {
			return nil, usageErrorf("item %d has no key", i)
		}
	}
	return items, nil
}

func importSapiItems(client *Client, s sapiSlice, items []sapiExportItem) (int, error) {
	target := s.path("/tool/sapi/data", nil)
	for i, item := range items {
		if _, err := client.PutJSON(target, map[string]any{"key": item.Key, "value": item.Value}); err != nil {
			return i, err
		}
	}
	return len(items), nil
}

func sapiGet(cmd *cobra.Command, path string, print func(map[string]any)) error {
	client, err := deployClient(cmd)
	if err != nil {
		return err
	}
	resp, err := client.GetJSON(path)
	if err != nil {
		return err
	}
	if jsonOutput {
		return emit(resp.Data)
	}
	print(resp.Data)
	return nil
}

func sapiSend(cmd *cobra.Command, method, path string, body any, done string) error {
	client, err := deployClient(cmd)
	if err != nil {
		return err
	}
	var resp *APIResponse
	switch method {
	case "PUT":
		resp, err = client.PutJSON(path, body)
	case "POST":
		resp, err = client.PostJSON(path, body)
	default:
		resp, err = client.Delete(path)
	}
	if err != nil {
		return err
	}
	if jsonOutput {
		return emit(resp.Data)
	}
	logf("%s\n", done)
	return nil
}

func shortChecksum(sum string) string {
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}

func printSapiStatus(d map[string]any) {
	logf("enabled %v  anonymous %v  plan %v\n", d["enabled"], d["anonymous_enabled"], d["plan"])
	if d["disabled_by_staff"] == true {
		logf("turned off by Blazium staff: %v\n", d["disabled_reason"])
	}
	logf("base url %v\n", d["base_url"])
	active, _ := d["active"].(map[string]any)
	channels := make([]string, 0, len(active))
	for ch := range active {
		channels = append(channels, ch)
	}
	sort.Strings(channels)
	for _, ch := range channels {
		logf("  %s -> %v\n", ch, shortChecksum(fmt.Sprint(active[ch])))
	}
	if len(channels) == 0 {
		logf("  no channel is serving a pack\n")
	}
	if s, ok := d["load_status"].(string); ok && s != "" {
		logf("load %s %v\n", s, d["load_error"])
	}
}

func printSapiPacks(d map[string]any) {
	list, _ := d["packs"].([]any)
	if len(list) == 0 {
		logf("no packs stored\n")
		return
	}
	for _, v := range list {
		p, _ := v.(map[string]any)
		logf("%v  %v  %v files  %v bytes  %v  active on %v\n", p["checksum"], p["channel"], p["files"], p["bytes"], p["created_at"], p["active_on"])
	}
}

func printSapiLogs(d map[string]any) {
	list, _ := d["lines"].([]any)
	for _, v := range list {
		l, _ := v.(map[string]any)
		logf("%v %-5v %v/%v %v %s\n", l["ts"], l["level"], l["channel"], l["logic"], l["request_id"], termSafe(fmt.Sprint(l["message"])))
	}
	if len(list) == 0 {
		logf("no log lines\n")
	}
	if next, _ := d["next_cursor"].(string); next != "" {
		logf("more: --cursor %s\n", next)
	}
}

func printSapiUsage(d map[string]any) {
	logf("plan %v  read-only %v\n", d["plan"], d["read_only"])
	usage, _ := d["usage"].(map[string]any)
	limits, _ := d["limits"].(map[string]any)
	for _, k := range []string{"total_bytes", "global_bytes", "global_keys", "users", "board_entries"} {
		logf("  %-14s %v / %v\n", k, usage[k], limits[k])
	}
}

func printSapiItems(d map[string]any) {
	items := sapiItems(d)
	for _, item := range items {
		ch := item["channel"]
		if ch == "" || ch == nil {
			ch = "*"
		}
		if v, ok := item["value"]; ok {
			raw, _ := json.Marshal(v)
			logf("%v  %v = %s  public=%v\n", ch, item["name"], raw, item["public"])
		} else {
			logf("%v  %v\n", ch, item["name"])
		}
	}
	if len(items) == 0 {
		logf("none\n")
	}
}

func printSapiJobs(d map[string]any) {
	schedules, _ := d["schedules"].([]any)
	for _, v := range schedules {
		s, _ := v.(map[string]any)
		logf("%v  %q  %v  paused=%v\n", s["logic"], s["cron"], s["channel"], s["paused"])
	}
	runs, _ := d["runs"].([]any)
	if len(runs) > 0 {
		logf("recent runs:\n")
	}
	for _, v := range runs {
		r, _ := v.(map[string]any)
		logf("  %v %v %v %s\n", r["logic"], r["channel"], r["status"], termSafe(fmt.Sprint(r["error"])))
	}
	if len(schedules) == 0 && len(runs) == 0 {
		logf("no scheduled logic\n")
	}
}

// sapiResetPath checks a reset request before anything is sent.
func sapiResetPath(scope, channel, user, prefix, confirm string, dry bool) (string, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	switch scope {
	case "":
		return "", usageErrorf("--scope is required: global, channel or user")
	case "global":
		if channel != "" || user != "" {
			return "", usageErrorf("the global scope takes no --channel or --user")
		}
	case "channel":
		if user != "" {
			return "", usageErrorf("the channel scope takes no --user")
		}
	case "user":
		if channel != "" {
			return "", usageErrorf("the user scope takes no --channel")
		}
		if user == "" && prefix != "" {
			return "", usageErrorf("--prefix needs one player: add --user, or drop --prefix to reset every player")
		}
	default:
		return "", usageErrorf("--scope must be global, channel or user")
	}
	ch, err := sapiOptionalChannel(channel)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(confirm) == "" {
		return "", usageErrorf("--confirm needs this game's id")
	}
	q := url.Values{"scope": {scope}, "confirm": {strings.TrimSpace(confirm)}}
	if ch != "" {
		q.Set("channel", ch)
	}
	if user != "" {
		q.Set("user", user)
	}
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	if dry {
		q.Set("dry_run", "true")
	}
	return "/tool/sapi/data?" + q.Encode(), nil
}

func printSapiReset(d map[string]any) {
	num := func(k string) int64 {
		v, _ := d[k].(float64)
		return int64(v)
	}
	verb := "Deleted"
	if dry, _ := d["dry_run"].(bool); dry {
		verb = "Would delete"
	}
	logf("%s %d keys (%d bytes)", verb, num("keys"), num("bytes"))
	if n := num("users"); n > 0 {
		logf(" across %d players", n)
	}
	if n := num("channels"); n > 0 {
		logf(" in %d channels", n)
	}
	logf("\n")
}

func printSapiData(d map[string]any) {
	items := sapiItems(d)
	for _, item := range items {
		raw, _ := json.Marshal(item["value"])
		logf("%s = %s\n", termSafe(fmt.Sprint(item["key"])), raw)
	}
	if len(items) == 0 {
		logf("no keys\n")
	}
	if next, _ := d["next"].(string); next != "" {
		logf("more after %s\n", next)
	}
}
