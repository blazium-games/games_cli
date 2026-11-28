package main

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// Starter scripts for the Scripted API. They are copied from games_sapi/templates (a test keeps the two in step) so
// chauffeur can scaffold a pack without the games_sapi repo. Scaffolding only adds files: a feature runs for a game
// once its owner turns it on in the dashboard.

//go:embed sapi_templates
var sapiTemplateFS embed.FS

const sapiTemplateRoot = "sapi_templates"

// sapiStarterFeatures have a starter script, in the order the catalog lists them.
var sapiStarterFeatures = []string{"hub", "daily", "wheel", "chests", "quests", "leaderboards", "competitions", "economy", "lives", "meta"}

// sapiCapabilityIDs are the bridges a script declares in its manifest `uses`. They have no starter script.
var sapiCapabilityIDs = []string{"items", "progress", "inbox", "profiles", "lobby", "lobby_data"}

// sapiFeatureRequires mirrors feature_catalog.json in games_service: what a feature cannot run without.
var sapiFeatureRequires = map[string][]string{"lives": {"economy"}}

const sapiDashboardLink = "https://blazium.games/edit"

var (
	sapiInitFeatures string
	sapiInitForce    bool
	sapiDevAll       bool
	sapiDevList      string
)

var sapiInitCmd = &cobra.Command{
	Use:   "init --dir <path>",
	Short: "Create a Scripted API pack with the shared helpers and the starter scripts you pick",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ids, err := sapiParseFeatureList(sapiInitFeatures)
		if err != nil {
			return err
		}
		return sapiRunScaffold(sapiDir, ids, sapiInitForce)
	},
}

var sapiFeatureAddCmd = &cobra.Command{
	Use:   "add <feature>...",
	Short: "Copy a feature's starter script and tests into the pack. The owner still has to turn the feature on",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ids, err := sapiParseFeatureList(strings.Join(args, ","))
		if err != nil {
			return err
		}
		for _, id := range ids {
			if !sapiHasStarter(id) {
				return usageErrorf("%s is a capability, not a feature, so it has no starter script. Declare it in the `uses` list of the script that needs it", id)
			}
		}
		return sapiRunScaffold(sapiDir, ids, sapiInitForce)
	},
}

func init() {
	sapiInitCmd.Flags().StringVar(&sapiDir, "dir", "", "Directory to create the pack in")
	_ = sapiInitCmd.MarkFlagRequired("dir")
	sapiInitCmd.Flags().StringVar(&sapiInitFeatures, "features", "hub", "Comma separated starter scripts to add: "+strings.Join(sapiStarterFeatures, ", "))
	sapiInitCmd.Flags().BoolVar(&sapiInitForce, "force", false, "Overwrite files that already exist")
	sapiFeatureAddCmd.Flags().StringVar(&sapiDir, "dir", ".", "Directory of the pack")
	sapiFeatureAddCmd.Flags().BoolVar(&sapiInitForce, "force", false, "Overwrite files that already exist")
	sapiDevCmd.Flags().BoolVar(&sapiDevAll, "all-features", false, "Run the emulator with every feature and capability on, as a game with feature management off would")
	sapiDevCmd.Flags().StringVar(&sapiDevList, "features", "", "Run the emulator as a managed game with only these features on (comma separated)")
	sapiFeatureCmd.AddCommand(sapiFeatureAddCmd)
	sapiCmd.AddCommand(sapiInitCmd)
}

func sapiKnownFeature(id string) bool {
	for _, list := range [][]string{sapiStarterFeatures, sapiCapabilityIDs} {
		for _, k := range list {
			if k == id {
				return true
			}
		}
	}
	return false
}

func sapiHasStarter(id string) bool {
	for _, k := range sapiStarterFeatures {
		if k == id {
			return true
		}
	}
	return false
}

// sapiParseFeatureList reads a comma separated list of feature ids. Unknown ids are refused so a typo cannot silently
// scaffold nothing; the order is the catalog's so output is the same however the list was typed.
func sapiParseFeatureList(raw string) ([]string, error) {
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }) {
		id := strings.ToLower(strings.TrimSpace(part))
		if id == "none" {
			continue
		}
		if !sapiKnownFeature(id) {
			return nil, usageErrorf("%q is not a feature. Features: %s. Capabilities: %s", part, strings.Join(sapiStarterFeatures, ", "), strings.Join(sapiCapabilityIDs, ", "))
		}
		seen[id] = true
	}
	var out []string
	for _, list := range [][]string{sapiStarterFeatures, sapiCapabilityIDs} {
		for _, id := range list {
			if seen[id] {
				out = append(out, id)
			}
		}
	}
	return out, nil
}

// sapiFeatureClosure adds what the listed features need.
func sapiFeatureClosure(ids []string) []string {
	have := map[string]bool{}
	for _, id := range ids {
		have[id] = true
		for _, req := range sapiFeatureRequires[id] {
			have[req] = true
		}
	}
	out, _ := sapiParseFeatureList(joinKeys(have))
	return out
}

func joinKeys(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// sapiDevFeatures is the SAPI_DEV_FEATURES value for the emulator. Empty means the emulator runs every feature, like a
// game that is not managed yet; --all-features says so explicitly; --features runs a managed game with only those on.
func sapiDevFeatures(all bool, list string) (string, error) {
	if all && strings.TrimSpace(list) != "" {
		return "", usageErrorf("use --all-features or --features, not both")
	}
	if all {
		ids := append(append([]string{}, sapiStarterFeatures...), sapiCapabilityIDs...)
		return strings.Join(ids, ","), nil
	}
	if strings.TrimSpace(list) == "" {
		return "", nil
	}
	ids, err := sapiParseFeatureList(list)
	if err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "none", nil
	}
	return strings.Join(sapiFeatureClosure(ids), ","), nil
}

// sapiScaffold writes the shared helpers and the starter scripts and tests for ids into dir. It never overwrites a file
// unless force is set, and says which it skipped. Capabilities have no starter script and are returned as notes.
func sapiScaffold(dir string, ids []string, force bool) (written, skipped []string, err error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil, usageErrorf("--dir is required")
	}
	wanted := map[string]bool{}
	for _, id := range sapiFeatureClosure(ids) {
		if sapiHasStarter(id) {
			wanted[id] = true
		}
	}
	var names []string
	err = fs.WalkDir(sapiTemplateFS, sapiTemplateRoot, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		rel := strings.TrimPrefix(p, sapiTemplateRoot+"/")
		if strings.HasPrefix(rel, "lib/") {
			names = append(names, rel)
			return nil
		}
		base := strings.TrimSuffix(strings.TrimSuffix(rel, ".luau"), ".test")
		if wanted[base] && !strings.Contains(rel, "/") {
			names = append(names, rel)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(names)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	for _, rel := range names {
		body, rerr := sapiTemplateFS.ReadFile(path.Join(sapiTemplateRoot, rel))
		if rerr != nil {
			return written, skipped, rerr
		}
		target := filepath.FromSlash(rel)
		if _, serr := root.Lstat(target); serr == nil && !force {
			skipped = append(skipped, rel)
			continue
		}
		if err := writeFileInRoot(root, target, body, 0o644); err != nil {
			return written, skipped, err
		}
		written = append(written, rel)
	}
	return written, skipped, nil
}

func sapiRunScaffold(dir string, ids []string, force bool) error {
	written, skipped, err := sapiScaffold(dir, ids, force)
	if err != nil {
		return err
	}
	if jsonOutput {
		return emit(map[string]any{"ok": true, "dir": dir, "written": written, "skipped": skipped, "features": ids})
	}
	for _, f := range written {
		logf("  wrote %s\n", f)
	}
	for _, f := range skipped {
		logf("  kept  %s (already there; --force overwrites)\n", f)
	}
	var caps []string
	for _, id := range ids {
		if !sapiHasStarter(id) {
			caps = append(caps, id)
		}
	}
	if len(caps) > 0 {
		logf("%s are capabilities: add them to the `uses` list of the scripts that need them.\n", strings.Join(caps, ", "))
	}
	logf("Scaffolding only adds files. Each feature runs once the game owner turns it on at %s (Scripted API page). Test locally with: chauffeur sapi dev --dir %s --all-features\n", sapiDashboardLink, dir)
	return nil
}

// sapiLogicFeatures names the features a pack's top-level logic files belong to, by file name.
func sapiLogicFeatures(files []lobbyFile) []string {
	var out []string
	for _, f := range files {
		if strings.Contains(f.name, "/") || strings.Contains(f.name, `\`) || strings.Contains(f.name, ".test.") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimSuffix(strings.ToLower(f.name), ".luau"), ".lua")
		if sapiHasStarter(id) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// warnDisabledFeatures tells the developer which of the pack's logic files belong to features that are off for the game.
// It is advice only: it never fails the command and says nothing when it cannot reach the service or the game is not
// managed yet.
func warnDisabledFeatures(client *Client, files []lobbyFile) {
	logics := sapiLogicFeatures(files)
	if len(logics) == 0 || client == nil {
		return
	}
	resp, err := client.GetJSON("/tool/sapi/features")
	if err != nil || resp == nil {
		return
	}
	for _, line := range sapiDisabledWarnings(resp.Data, logics) {
		fmt.Fprintln(stderr, line)
	}
}

func sapiDisabledWarnings(data map[string]any, logics []string) []string {
	if managed, _ := data["managed"].(bool); !managed {
		return nil
	}
	state := map[string]map[string]any{}
	items, _ := data["features"].([]any)
	for _, v := range items {
		if f, _ := v.(map[string]any); f != nil {
			state[fmt.Sprint(f["id"])] = f
		}
	}
	link, _ := data["enable_url"].(string)
	if link == "" {
		link = sapiDashboardLink
	}
	var off []string
	for _, id := range logics {
		f, ok := state[id]
		if !ok {
			continue
		}
		reason := ""
		switch {
		case f["staff_disabled"] == true:
			reason = id + " (stopped by staff)"
		case f["active"] != true:
			reason = id
		}
		if reason != "" {
			off = append(off, reason)
		}
	}
	if len(off) == 0 {
		return nil
	}
	return []string{
		"Warning: these logic files belong to features that are off for this game, so players get feature_disabled: " + strings.Join(off, ", "),
		"The game owner can turn them on at " + link,
	}
}
