package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// patronHTTPClient is the timed client for account API calls. Tests replace it.
var patronHTTPClient = newHTTPClient

func init() {
	patronCmd.PersistentFlags().String("user-token", "", "Account access token (overrides BLAZIUM_USER_TOKEN). This is not a deploy key.")
	patronStatusCmd.Flags().String("developer", "", "Developer username. Empty lists every membership")
	tierCreateCmd.Flags().String("name", "", "Tier name")
	tierCreateCmd.Flags().Int("rank", 1, "Rank. A higher rank includes every lower rank")
	tierCreateCmd.Flags().Int64("price-cents", 0, "Price in cents, at least 50")
	tierCreateCmd.Flags().String("billing", "monthly", "monthly or once")
	tierCreateCmd.Flags().String("unlock", "", "For a one-time tier: lifetime or until")
	tierCreateCmd.Flags().String("until", "", "RFC3339 cutoff when unlock is until")
	tierUpdateCmd.Flags().String("name", "", "New name")
	tierUpdateCmd.Flags().Int64("price-cents", 0, "New price in cents for future pledges")
	tierUpdateCmd.Flags().String("active", "", "true or false. Empty leaves the current value")
	postWriteFlags(postCreateCmd)
	postWriteFlags(postUpdateCmd)
	rootCmd.AddCommand(patronCmd)
	patronCmd.AddCommand(patronStatusCmd, patronListCmd, patronGetCmd, patronTiersCmd, patronTierCmd, patronPostsCmd, patronPostCmd)
	patronTierCmd.AddCommand(tierCreateCmd, tierUpdateCmd, tierDeleteCmd)
	patronPostCmd.AddCommand(postCreateCmd, postUpdateCmd, postDeleteCmd)
}

func postWriteFlags(cmd *cobra.Command) {
	cmd.Flags().String("title", "", "Post title")
	cmd.Flags().String("body", "", "Post body")
	cmd.Flags().String("status", "draft", "draft or published")
	cmd.Flags().String("visibility", "public", "public or tiers")
	cmd.Flags().StringSlice("tier", nil, "Tier uid that can read a tiers post. Repeat for more than one")
}

var patronCmd = &cobra.Command{Use: "patron", Short: "Patron tiers, posts, and memberships for the signed-in account"}

var patronStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the signed-in account's patron memberships",
	RunE: func(cmd *cobra.Command, _ []string) error {
		developer, _ := cmd.Flags().GetString("developer")
		path := patronQuery("/private/patron/status", "developer", developer)
		return printUserAPI(cmd, http.MethodGet, path, nil)
	},
}

var patronListCmd = &cobra.Command{
	Use:   "list",
	Short: "List patrons of the signed-in developer",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return printUserAPI(cmd, http.MethodGet, "/private/patrons", nil)
	},
}

var patronGetCmd = &cobra.Command{
	Use:   "get <username>",
	Short: "Show one patron of the signed-in developer",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return printUserAPI(cmd, http.MethodGet, patronResource("/private/patrons", args[0]), nil)
	},
}

var patronTiersCmd = &cobra.Command{
	Use:   "tiers",
	Short: "List tiers and proposed game links",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return printUserAPI(cmd, http.MethodGet, "/private/tiers", nil)
	},
}

var patronTierCmd = &cobra.Command{Use: "tier", Short: "Create, update, or delete a tier"}

var tierCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a tier. Rank, billing, and unlock stay fixed after this",
	RunE: func(cmd *cobra.Command, _ []string) error {
		name, _ := cmd.Flags().GetString("name")
		rank, _ := cmd.Flags().GetInt("rank")
		price, _ := cmd.Flags().GetInt64("price-cents")
		billing, _ := cmd.Flags().GetString("billing")
		unlock, _ := cmd.Flags().GetString("unlock")
		until, _ := cmd.Flags().GetString("until")
		body := map[string]any{"name": name, "rank": rank, "price_cents": price, "billing": billing, "unlock": unlock}
		if until != "" {
			body["until"] = until
		}
		return printUserAPI(cmd, http.MethodPost, "/private/tiers", body)
	},
}

var tierUpdateCmd = &cobra.Command{
	Use:   "update <uid>",
	Short: "Change a tier's name, price, or whether it is shown",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		body, err := tierUpdateBody(cmd)
		if err != nil {
			return err
		}
		return printUserAPI(cmd, http.MethodPut, patronResource("/private/tiers", args[0]), body)
	},
}

var tierDeleteCmd = &cobra.Command{
	Use:   "delete <uid>",
	Short: "Delete a tier that has no pending or active membership",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return printUserAPI(cmd, http.MethodDelete, patronResource("/private/tiers", args[0]), nil)
	},
}

var patronPostsCmd = &cobra.Command{
	Use:   "posts",
	Short: "List draft and published posts",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return printUserAPI(cmd, http.MethodGet, "/private/posts", nil)
	},
}

var patronPostCmd = &cobra.Command{Use: "post", Short: "Create, update, or delete a post"}

func postBody(cmd *cobra.Command) map[string]any {
	title, _ := cmd.Flags().GetString("title")
	body, _ := cmd.Flags().GetString("body")
	status, _ := cmd.Flags().GetString("status")
	visibility, _ := cmd.Flags().GetString("visibility")
	tiers, _ := cmd.Flags().GetStringSlice("tier")
	return map[string]any{"title": title, "body": body, "status": status, "visibility": visibility, "tier_uids": tiers}
}

func postUpdateBody(cmd *cobra.Command) (map[string]any, error) {
	body := map[string]any{}
	if cmd.Flags().Changed("title") {
		title, _ := cmd.Flags().GetString("title")
		body["title"] = title
	}
	if cmd.Flags().Changed("body") {
		text, _ := cmd.Flags().GetString("body")
		body["body"] = text
	}
	if cmd.Flags().Changed("status") {
		status, _ := cmd.Flags().GetString("status")
		if status != "draft" && status != "published" {
			return nil, usageErrorf("--status must be draft or published")
		}
		body["status"] = status
	}
	if cmd.Flags().Changed("visibility") {
		visibility, _ := cmd.Flags().GetString("visibility")
		if visibility != "public" && visibility != "tiers" {
			return nil, usageErrorf("--visibility must be public or tiers")
		}
		body["visibility"] = visibility
	}
	if cmd.Flags().Changed("tier") {
		tiers, _ := cmd.Flags().GetStringSlice("tier")
		body["tier_uids"] = tiers
	}
	if len(body) == 0 {
		return nil, usageErrorf("pass --title, --body, --status, --visibility, or --tier")
	}
	return body, nil
}

func tierUpdateBody(cmd *cobra.Command) (map[string]any, error) {
	body := map[string]any{}
	if cmd.Flags().Changed("name") {
		name, _ := cmd.Flags().GetString("name")
		if strings.TrimSpace(name) == "" {
			return nil, usageErrorf("--name cannot be empty")
		}
		body["name"] = name
	}
	if cmd.Flags().Changed("price-cents") {
		price, _ := cmd.Flags().GetInt64("price-cents")
		if price < 50 {
			return nil, usageErrorf("--price-cents must be at least 50")
		}
		body["price_cents"] = price
	}
	if cmd.Flags().Changed("active") {
		active, _ := cmd.Flags().GetString("active")
		on, err := strconv.ParseBool(active)
		if err != nil {
			return nil, usageErrorf("--active must be true or false")
		}
		body["active"] = on
	}
	if len(body) == 0 {
		return nil, usageErrorf("pass --name, --price-cents, or --active")
	}
	return body, nil
}

func patronResource(base, id string) string {
	return strings.TrimRight(base, "/") + "/" + url.PathEscape(id)
}

func patronQuery(path, key, value string) string {
	if value == "" {
		return path
	}
	return path + "?" + key + "=" + url.QueryEscape(value)
}

var postCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a public or tier-locked post",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return printUserAPI(cmd, http.MethodPost, "/private/posts", postBody(cmd))
	},
}

var postUpdateCmd = &cobra.Command{
	Use:   "update <uid>",
	Short: "Edit a post",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		body, err := postUpdateBody(cmd)
		if err != nil {
			return err
		}
		return printUserAPI(cmd, http.MethodPut, patronResource("/private/posts", args[0]), body)
	},
}

var postDeleteCmd = &cobra.Command{
	Use:   "delete <uid>",
	Short: "Remove a post",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return printUserAPI(cmd, http.MethodDelete, patronResource("/private/posts", args[0]), nil)
	},
}

func printUserAPI(cmd *cobra.Command, method, path string, payload any) error {
	token, _ := cmd.Flags().GetString("user-token")
	if strings.TrimSpace(token) == "" {
		token = os.Getenv("BLAZIUM_USER_TOKEN")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return usageErrorf("a user access token is required: set BLAZIUM_USER_TOKEN or pass --user-token. A deploy key cannot call this")
	}
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	base := strings.TrimRight(apiURL, "/")
	req, err := http.NewRequest(method, base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := patronHTTPClient().Do(req)
	if err != nil {
		return &networkError{err: err}
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return fmt.Errorf("patron request failed: %s", strings.TrimSpace(string(raw)))
	}
	if jsonOutput {
		fmt.Println(string(raw))
		return nil
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if json.Unmarshal(raw, &env) == nil && env.Data != nil {
		out, _ := json.MarshalIndent(env.Data, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	fmt.Println(string(raw))
	return nil
}
