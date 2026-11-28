package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// chauffeur lobby data|config|secret|usage manage the store of games_slobby (Grow and Scale). It is a separate store from
// the Scripted API's, with the same admin routes, so these commands mirror chauffeur sapi data|config|secret|usage and add
// the lobby scope.

const lobbyDataBase = "/tool/lobby-data"

var lobbyIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

var (
	lobbyDataScope   string
	lobbyDataChannel string
	lobbyDataUser    string
	lobbyDataLobby   string
	lobbyDataPrefix  string
	lobbyDataLimit   int
	lobbyDataTTL     int64
	lobbyDataOut     string

	lobbyCfgChannel string
	lobbyCfgPublic  bool
	lobbySecretFile string

	lobbyResetScope   string
	lobbyResetChannel string
	lobbyResetUser    string
	lobbyResetLobby   string
	lobbyResetPrefix  string
	lobbyResetConfirm string
	lobbyResetDry     bool
)

var lobbyDataCmd = &cobra.Command{Use: "data", Short: "Read and change stored lobby data (separate from the Scripted API's data)"}

var lobbyDataListCmd = &cobra.Command{
	Use:   "list",
	Short: "List keys in a scope",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := lobbySliceFlags()
		if err != nil {
			return err
		}
		return sapiGet(cmd, s.listPath(lobbyDataPrefix, "", lobbyDataLimit), printSapiData)
	},
}

var lobbyDataGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Print one stored value",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := lobbySliceFlags()
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
				fmt.Fprintln(stdout, string(raw))
				return nil
			}
		}
		return &apiError{Status: 404, Code: 4040, Message: "no value is stored under " + args[0]}
	},
}

var lobbyDataSetCmd = &cobra.Command{
	Use:   "set <key> <json value>",
	Short: "Store a value (plain text is stored as a string)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := lobbySliceFlags()
		if err != nil {
			return err
		}
		body := map[string]any{"key": args[0], "value": parseSapiValue(args[1])}
		if lobbyDataTTL > 0 {
			body["ttl"] = lobbyDataTTL
		}
		return sapiSend(cmd, "PUT", s.path(lobbyDataBase+"/data", nil), body, "Stored "+args[0])
	},
}

var lobbyDataDeleteCmd = &cobra.Command{
	Use:   "delete <key>",
	Short: "Delete one stored value",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := lobbySliceFlags()
		if err != nil {
			return err
		}
		return sapiSend(cmd, "DELETE", s.path(lobbyDataBase+"/data/key", url.Values{"key": {args[0]}}), nil, "Deleted "+args[0])
	},
}

var lobbyDataExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Write every key in a scope as JSON (to --out or stdout)",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := lobbySliceFlags()
		if err != nil {
			return err
		}
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		items, err := exportLobbySlice(client, s, lobbyDataPrefix)
		if err != nil {
			return err
		}
		raw, _ := json.MarshalIndent(items, "", "  ")
		if lobbyDataOut == "" {
			fmt.Fprintln(stdout, string(raw))
			return nil
		}
		if err := writeFileNoFollow(lobbyDataOut, append(raw, '\n'), 0o600); err != nil {
			return err
		}
		logf("Wrote %d keys to %s\n", len(items), lobbyDataOut)
		return nil
	},
}

var lobbyDataResetCmd = &cobra.Command{
	Use:   "reset --scope global|channel|user|lobby --confirm <game id>",
	Short: "Delete many stored values at once (use --dry-run to count first)",
	Long: "Deletes every key in a scope, or only the keys that start with --prefix. With --scope channel and no --channel it covers every channel; " +
		"with --scope user and no --user it covers every player; --scope lobby needs --lobby. --dry-run only counts what would be removed. " +
		"--confirm must be this game's id. The whole-game purge is not available here; use the dashboard or MCP.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := lobbyResetPath(lobbyResetScope, lobbyResetChannel, lobbyResetUser, lobbyResetLobby, lobbyResetPrefix, lobbyResetConfirm, lobbyResetDry)
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

var lobbyConfigCmd = &cobra.Command{Use: "config", Short: "Read and change lobby remote config values"}

var lobbyConfigGetCmd = &cobra.Command{
	Use:   "get",
	Short: "List lobby config values",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sapiGet(cmd, lobbyDataBase+"/config", printSapiItems)
	},
}

var lobbyConfigSetCmd = &cobra.Command{
	Use:   "set <name> <json value>",
	Short: "Set a lobby config value (plain text is stored as a string)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		body, err := sapiConfigBody(args[0], args[1], lobbyCfgChannel, lobbyCfgPublic)
		if err != nil {
			return err
		}
		return sapiSend(cmd, "PUT", lobbyDataBase+"/config", body, "Saved lobby config "+args[0])
	},
}

var lobbyConfigDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a lobby config value",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := sapiNamedPath(lobbyDataBase+"/config", args[0], lobbyCfgChannel)
		if err != nil {
			return err
		}
		return sapiSend(cmd, "DELETE", path, nil, "Deleted lobby config "+args[0])
	},
}

var lobbySecretCmd = &cobra.Command{Use: "secret", Short: "Store write-only secrets for lobby scripts"}

var lobbySecretListCmd = &cobra.Command{
	Use:   "list",
	Short: "List lobby secret names (values are never shown)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sapiGet(cmd, lobbyDataBase+"/secrets", printSapiItems)
	},
}

var lobbySecretSetCmd = &cobra.Command{
	Use:   "set <name>",
	Short: "Store a lobby secret; the value is read from stdin or --from-file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !sapiNameRe.MatchString(args[0]) {
			return usageErrorf("secret names use letters, digits, _ - . and at most 64 characters")
		}
		if fromStdin, _ := cmd.Flags().GetBool("secret-stdin"); fromStdin && lobbySecretFile == "" {
			return usageErrorf("the secret value is read from stdin, so set BLAZIUM_SECRET_KEY or pass --from-file")
		}
		value, err := readSapiSecretValue(lobbySecretFile, os.Stdin)
		if err != nil {
			return err
		}
		if len(value) > 4096 {
			return usageErrorf("lobby secret values are at most 4 KiB")
		}
		channel, err := sapiOptionalChannel(lobbyCfgChannel)
		if err != nil {
			return err
		}
		return sapiSend(cmd, "PUT", lobbyDataBase+"/secrets", map[string]any{"channel": channel, "name": args[0], "value": value}, "Stored lobby secret "+args[0])
	},
}

var lobbySecretDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a lobby secret",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := sapiNamedPath(lobbyDataBase+"/secrets", args[0], lobbyCfgChannel)
		if err != nil {
			return err
		}
		return sapiSend(cmd, "DELETE", path, nil, "Deleted lobby secret "+args[0])
	},
}

var lobbyUsageCmd = &cobra.Command{
	Use:   "usage",
	Short: "Show stored lobby data against the plan's limits",
	RunE: func(cmd *cobra.Command, args []string) error {
		return sapiGet(cmd, lobbyDataBase+"/usage", printLobbyUsage)
	},
}

func init() {
	for _, c := range []*cobra.Command{lobbyDataListCmd, lobbyDataGetCmd, lobbyDataSetCmd, lobbyDataDeleteCmd, lobbyDataExportCmd} {
		c.Flags().StringVar(&lobbyDataScope, "scope", "global", "global, channel, user or lobby")
		c.Flags().StringVar(&lobbyDataChannel, "channel", "", "Release channel for the channel scope (default stable)")
		c.Flags().StringVar(&lobbyDataUser, "user", "", "User id for the user scope")
		c.Flags().StringVar(&lobbyDataLobby, "lobby", "", "Lobby id for the lobby scope")
	}
	lobbyDataListCmd.Flags().StringVar(&lobbyDataPrefix, "prefix", "", "Only keys starting with this")
	lobbyDataListCmd.Flags().IntVar(&lobbyDataLimit, "limit", 50, "Most keys to return")
	lobbyDataExportCmd.Flags().StringVar(&lobbyDataPrefix, "prefix", "", "Only keys starting with this")
	lobbyDataExportCmd.Flags().StringVar(&lobbyDataOut, "out", "", "File to write (default stdout)")
	lobbyDataSetCmd.Flags().Int64Var(&lobbyDataTTL, "ttl", 0, "Seconds until the value expires (0 keeps it)")

	lobbyDataResetCmd.Flags().StringVar(&lobbyResetScope, "scope", "", "global, channel, user or lobby (required)")
	lobbyDataResetCmd.Flags().StringVar(&lobbyResetChannel, "channel", "", "Only this release channel (channel scope)")
	lobbyDataResetCmd.Flags().StringVar(&lobbyResetUser, "user", "", "Only this player (user scope)")
	lobbyDataResetCmd.Flags().StringVar(&lobbyResetLobby, "lobby", "", "The lobby to clear (lobby scope)")
	lobbyDataResetCmd.Flags().StringVar(&lobbyResetPrefix, "prefix", "", "Only keys starting with this")
	lobbyDataResetCmd.Flags().StringVar(&lobbyResetConfirm, "confirm", "", "This game's id (required)")
	lobbyDataResetCmd.Flags().BoolVar(&lobbyResetDry, "dry-run", false, "Count what would be removed and change nothing")

	for _, c := range []*cobra.Command{lobbyConfigSetCmd, lobbyConfigDeleteCmd, lobbySecretSetCmd, lobbySecretDeleteCmd} {
		c.Flags().StringVar(&lobbyCfgChannel, "channel", "", "Release channel (empty applies to every channel)")
	}
	lobbyConfigSetCmd.Flags().BoolVar(&lobbyCfgPublic, "public", false, "Let game clients read this value")
	lobbySecretSetCmd.Flags().StringVar(&lobbySecretFile, "from-file", "", "Read the value from this file instead of stdin")

	lobbyDataCmd.AddCommand(lobbyDataListCmd, lobbyDataGetCmd, lobbyDataSetCmd, lobbyDataDeleteCmd, lobbyDataExportCmd, lobbyDataResetCmd)
	lobbyConfigCmd.AddCommand(lobbyConfigGetCmd, lobbyConfigSetCmd, lobbyConfigDeleteCmd)
	lobbySecretCmd.AddCommand(lobbySecretListCmd, lobbySecretSetCmd, lobbySecretDeleteCmd)
	lobbyCmd.AddCommand(lobbyDataCmd, lobbyConfigCmd, lobbySecretCmd, lobbyUsageCmd)
}

// lobbySlice names one scope of the lobby store. It is a sapiSlice plus the lobby id.
type lobbySlice struct {
	sapiSlice
	lobby string
}

func lobbySliceFlags() (lobbySlice, error) {
	return newLobbySlice(lobbyDataScope, lobbyDataChannel, lobbyDataUser, lobbyDataLobby)
}

func newLobbySlice(scope, channel, user, lobby string) (lobbySlice, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	lobby = strings.TrimSpace(lobby)
	if scope == "lobby" {
		if !lobbyIDRe.MatchString(lobby) {
			return lobbySlice{}, usageErrorf("the lobby scope needs --lobby with a lobby id")
		}
		if strings.TrimSpace(channel) != "" || strings.TrimSpace(user) != "" {
			return lobbySlice{}, usageErrorf("the lobby scope takes no --channel or --user")
		}
		return lobbySlice{sapiSlice: sapiSlice{scope: "lobby"}, lobby: lobby}, nil
	}
	if lobby != "" {
		return lobbySlice{}, usageErrorf("--lobby only applies to --scope lobby")
	}
	base, err := newSapiSlice(scope, channel, user)
	if err != nil {
		return lobbySlice{}, usageErrorf("%s", strings.Replace(err.Error(), "global, channel or user", "global, channel, user or lobby", 1))
	}
	return lobbySlice{sapiSlice: base}, nil
}

func (s lobbySlice) path(base string, extra url.Values) string {
	q := url.Values{}
	if s.lobby != "" {
		q.Set("lobby", s.lobby)
	}
	for k, v := range extra {
		q[k] = v
	}
	return s.sapiSlice.path(base, q)
}

func (s lobbySlice) listPath(prefix, after string, limit int) string {
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
	return s.path(lobbyDataBase+"/data", extra)
}

func exportLobbySlice(client *Client, s lobbySlice, prefix string) ([]sapiExportItem, error) {
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

// lobbyResetPath checks a reset request before anything is sent.
func lobbyResetPath(scope, channel, user, lobby, prefix, confirm string, dry bool) (string, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	lobby = strings.TrimSpace(lobby)
	if scope == "lobby" {
		if !lobbyIDRe.MatchString(lobby) {
			return "", usageErrorf("--scope lobby needs --lobby with a lobby id")
		}
		if channel != "" || user != "" {
			return "", usageErrorf("the lobby scope takes no --channel or --user")
		}
		if strings.TrimSpace(confirm) == "" {
			return "", usageErrorf("--confirm needs this game's id")
		}
		q := url.Values{"scope": {"lobby"}, "lobby": {lobby}, "confirm": {strings.TrimSpace(confirm)}}
		if prefix != "" {
			q.Set("prefix", prefix)
		}
		if dry {
			q.Set("dry_run", "true")
		}
		return lobbyDataBase + "/data?" + q.Encode(), nil
	}
	if lobby != "" {
		return "", usageErrorf("--lobby only applies to --scope lobby")
	}
	path, err := sapiResetPath(scope, channel, user, prefix, confirm, dry)
	if err != nil {
		return "", usageErrorf("%s", strings.Replace(err.Error(), "global, channel or user", "global, channel, user or lobby", 1))
	}
	return strings.Replace(path, "/tool/sapi/data", lobbyDataBase+"/data", 1), nil
}

func printLobbyUsage(d map[string]any) {
	logf("plan %v  read-only %v\n", d["plan"], d["read_only"])
	usage, _ := d["usage"].(map[string]any)
	limits, _ := d["limits"].(map[string]any)
	for _, k := range []string{"total_bytes", "global_bytes", "global_keys", "users", "board_entries"} {
		logf("  %-14s %v / %v\n", k, usage[k], limits[k])
	}
	if v, ok := limits["lobby_data_bytes"]; ok {
		logf("  %-14s %v per lobby\n", "lobby_bytes", v)
	}
}
