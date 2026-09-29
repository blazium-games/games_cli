package main

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"
)

var infoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show the game that owns the deploy key",
	Long: `Show the game the deploy key belongs to: name, store URL, visibility, listing
readiness (what still blocks a public listing), images, build count, channels,
and the upload limits the service enforces.

Use it to check a key works before a CI run.`,
	Example: "  chauffeur info\n  chauffeur info --json",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := deployClient(cmd)
		if err != nil {
			return err
		}
		resp, err := c.GetJSON("/tool/info")
		if err != nil {
			return err
		}
		if jsonOutput {
			return emit(map[string]any{"ok": true, "info": resp.Data})
		}
		d := resp.Data
		logf("Game:       %v (%v)\n", d["name"], d["game_uid"])
		logf("Store page: %v\n", d["url"])
		logf("Visibility: %v, status %v\n", d["visibility"], d["status"])
		logf("Builds:     %v\n", d["builds"])
		if chs, _ := d["channels"].([]any); len(chs) > 0 {
			for _, ch := range chs {
				m, _ := ch.(map[string]any)
				logf("  channel %-10v -> build %v\n", m["channel"], m["build_id"])
			}
		}
		if lint, _ := d["lint"].(map[string]any); lint != nil {
			ready, _ := lint["ready"].(bool)
			logf("Listing ready: %v\n", ready)
			for _, key := range []string{"errors", "warnings"} {
				items, _ := lint[key].([]any)
				for _, it := range items {
					logf("  %s: %v\n", key[:len(key)-1], lintText(it))
				}
			}
		}
		if media, _ := d["media"].(map[string]any); media != nil {
			printMedia(parseMedia(media))
		}
		return nil
	},
}

func lintText(v any) string {
	if m, ok := v.(map[string]any); ok {
		if msg, ok := m["message"].(string); ok {
			return msg
		}
	}
	return fmt.Sprint(v)
}

var buildsCmd = &cobra.Command{
	Use:   "builds",
	Short: "Inspect uploaded builds",
}

var buildsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List builds with their files and symbol counts",
	Long: `List the game's builds, newest first, with their files and how many symbol
files each has. Filters are optional and combine.

  --os       ` + osHelp + `
  --arch     ` + archHelp + `
  --channel  ` + channelHelp,
	Example: `  chauffeur builds list
  chauffeur builds list --os windows --channel beta
  chauffeur builds list --version 1.2.0 --json`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		osName, _ := cmd.Flags().GetString("os")
		arch, _ := cmd.Flags().GetString("arch")
		channel, _ := cmd.Flags().GetString("channel")
		ver, _ := cmd.Flags().GetString("version")
		page, _ := cmd.Flags().GetInt("page")
		size, _ := cmd.Flags().GetInt("page-size")
		osName, arch, channel, err := validateFilter(osName, arch, channel)
		if err != nil {
			return err
		}
		if len(ver) > maxVersionLen {
			return usageErrorf("--version must be at most %d characters", maxVersionLen)
		}
		if page < 1 || page > 10000 {
			return usageErrorf("--page must be 1-10000")
		}
		if size < 1 || size > 100 {
			return usageErrorf("--page-size must be 1-100")
		}
		q := url.Values{}
		for k, v := range map[string]string{"os": osName, "arch": arch, "channel": channel, "version": ver} {
			if v != "" {
				q.Set(k, v)
			}
		}
		q.Set("page", strconv.Itoa(page))
		q.Set("page_size", strconv.Itoa(size))
		c, err := deployClient(cmd)
		if err != nil {
			return err
		}
		resp, err := c.GetJSON("/tool/builds?" + q.Encode())
		if err != nil {
			return err
		}
		if jsonOutput {
			return emit(map[string]any{"ok": true, "builds": resp.Data["builds"], "page": resp.Data["page"],
				"page_size": resp.Data["page_size"], "total": resp.Data["total"]})
		}
		builds, _ := resp.Data["builds"].([]any)
		logf("%v build(s), page %v\n", resp.Data["total"], resp.Data["page"])
		for _, b := range builds {
			m, _ := b.(map[string]any)
			logf("%v  %v  %v/%v  channel=%v  engine=%v  symbols=%v\n", m["build_id"], m["version"], m["os"], m["arch"], m["channel"], m["engine_version"], m["symbols"])
			files, _ := m["files"].([]any)
			for _, f := range files {
				fm, _ := f.(map[string]any)
				logf("    %v  %v\n", fm["filename"], fm["checksum"])
			}
		}
		return nil
	},
}

func init() {
	f := buildsListCmd.Flags()
	f.String("os", "", "Only this OS: "+osHelp)
	f.String("arch", "", "Only this arch: "+archHelp)
	f.String("channel", "", "Only this channel: "+channelHelp)
	f.String("version", "", "Only this version (exact match)")
	f.Int("page", 1, "Page number (1-10000)")
	f.Int("page-size", 20, "Builds per page (1-100)")
	buildsCmd.AddCommand(buildsListCmd)
}
