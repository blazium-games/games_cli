package main

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

// chauffeur sapi features and sapi feature status|enable|disable|history. A feature only works for a game once its owner
// turns it on in the dashboard. A deploy key can list features and turn them off (so CI can fail safe) but never on.

var sapiFeatureIDRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

var sapiFeatureCascade bool

var sapiFeaturesCmd = &cobra.Command{
	Use:   "features",
	Short: "List the Scripted API features and which ones are enabled for this game",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return sapiGet(cmd, "/tool/sapi/features", printSapiFeatures)
	},
}

var sapiFeatureCmd = &cobra.Command{Use: "feature", Short: "Check or turn off one Scripted API feature"}

var sapiFeatureStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show what is enabled, and what needs the owner",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return sapiGet(cmd, "/tool/sapi/features", printSapiFeatureStatus)
	},
}

var sapiFeatureEnableCmd = &cobra.Command{
	Use:   "enable <feature>",
	Short: "Explain how to turn a feature on (only the game owner can, from the dashboard)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := sapiFeaturePath(args[0], "enable"); err != nil {
			return err
		}
		link := "https://blazium.games/edit"
		client, err := deployClient(cmd)
		if err == nil {
			if resp, gerr := client.GetJSON("/tool/sapi/features"); gerr == nil {
				if u, _ := resp.Data["enable_url"].(string); u != "" {
					link = u
				}
			}
		}
		return usageErrorf("a deploy key cannot turn %s on. Ask the game owner to enable it on the Scripted API page: %s\n"+
			"Scaffolding a feature only adds files; nothing works until the owner enables it.", args[0], link)
	},
}

var sapiFeatureDisableCmd = &cobra.Command{
	Use:   "disable <feature>",
	Short: "Turn a feature off. Data is kept and schedules pause; the owner can turn it back on",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := sapiFeaturePath(args[0], "disable")
		if err != nil {
			return err
		}
		if sapiFeatureCascade {
			path += "?cascade=true"
		}
		return sapiSend(cmd, "POST", path, map[string]any{"cascade": sapiFeatureCascade}, "Turned "+args[0]+" off")
	},
}

var sapiFeatureHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "Show who turned features on or off",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return sapiGet(cmd, "/tool/sapi/features/history", printSapiFeatureHistory)
	},
}

func init() {
	sapiFeatureDisableCmd.Flags().BoolVar(&sapiFeatureCascade, "cascade", false, "Also turn off the enabled features that need this one")
	sapiFeatureCmd.AddCommand(sapiFeatureStatusCmd, sapiFeatureEnableCmd, sapiFeatureDisableCmd, sapiFeatureHistoryCmd)
	sapiCmd.AddCommand(sapiFeaturesCmd, sapiFeatureCmd)
}

// sapiFeaturePath builds /tool/sapi/features/{id}/{verb} after checking the id.
func sapiFeaturePath(id, verb string) (string, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if !sapiFeatureIDRe.MatchString(id) {
		return "", usageErrorf("feature ids use lowercase letters, digits and _, like daily or lobby_data")
	}
	return "/tool/sapi/features/" + url.PathEscape(id) + "/" + verb, nil
}

func printSapiFeatures(d map[string]any) {
	items, _ := d["features"].([]any)
	if managed, _ := d["managed"].(bool); !managed {
		logf("Feature management is off for this game, so every feature runs. The owner can turn it on from the dashboard.\n")
	}
	for _, v := range items {
		f, _ := v.(map[string]any)
		state := "off"
		switch {
		case f["staff_disabled"] == true:
			state = "stopped by staff"
		case f["enabled"] == true:
			state = "on"
		case f["active"] == true && f["kind"] == "capability":
			state = "on (bridged)"
		}
		notes := ""
		if f["sensitive"] == true {
			notes += " sensitive"
		}
		if f["plan_ok"] == false {
			notes += " needs " + fmt.Sprint(f["min_plan"])
		}
		logf("  %-13s %-10s %-16s%s\n", f["id"], f["kind"], state, notes)
	}
}

func printSapiFeatureStatus(d map[string]any) {
	items, _ := d["features"].([]any)
	var on, off []string
	for _, v := range items {
		f, _ := v.(map[string]any)
		id := fmt.Sprint(f["id"])
		if f["enabled"] == true && f["staff_disabled"] != true {
			on = append(on, id)
		} else if f["kind"] == "feature" {
			off = append(off, id)
		}
	}
	managed, _ := d["managed"].(bool)
	logf("managed: %v\n", managed)
	logf("enabled: %s\n", joinOrNone(on))
	logf("not enabled: %s\n", joinOrNone(off))
	if note, _ := d["note"].(string); note != "" {
		logf("%s\n", note)
	}
}

func printSapiFeatureHistory(d map[string]any) {
	events, _ := d["events"].([]any)
	for _, v := range events {
		e, _ := v.(map[string]any)
		logf("%v  %-14v %-8v %v via %v\n", e["created_at"], e["feature"], e["action"], e["actor"], e["source"])
	}
	if len(events) == 0 {
		logf("no changes yet\n")
	}
}

func joinOrNone(list []string) string {
	if len(list) == 0 {
		return "none"
	}
	return strings.Join(list, ", ")
}
