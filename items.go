package main

import (
	"encoding/json"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	itemsCmd.AddCommand(itemsListCmd, itemsCreateCmd, itemsEditCmd, itemsIconCmd, itemsSchemaCmd, itemsGiveCmd, itemsSetCmd)
	mintKeyCmd.AddCommand(mintKeyListCmd, mintKeyCreateCmd, mintKeyRenameCmd, mintKeyRevokeCmd)
	itemsCreateCmd.Flags().Int64("itemdef", 0, "Steam itemdef id")
	itemsCreateCmd.Flags().String("name", "", "Item name")
	itemsCreateCmd.Flags().String("platform-type", "material", "melee, ranged, armor, jewelry, shield, consumable, material, blueprint, food, or drink")
	itemsCreateCmd.Flags().String("steam-type", "item", "item, bundle, generator, or playtimegenerator")
	itemsCreateCmd.Flags().String("stats", "{}", "Base stats as JSON")
	itemsCreateCmd.Flags().Bool("stackable", false, "Group plain copies for display")
	itemsCreateCmd.Flags().Bool("unique", false, "This item can be minted once, ever")
	itemsCreateCmd.Flags().Bool("socketable", false, "Other items can be attached")
	itemsCreateCmd.Flags().Int("sockets", 0, "How many sockets")
	itemsCreateCmd.Flags().String("accepts", "[]", "JSON list of platform types this item accepts")
	itemsCreateCmd.Flags().Bool("tradable", false, "Players can list this item")
	itemsEditCmd.Flags().Int64("itemdef", 0, "Steam itemdef id")
	itemsEditCmd.Flags().String("name", "", "Item name")
	itemsEditCmd.Flags().String("platform-type", "", "Platform type")
	itemsEditCmd.Flags().String("stats", "", "Base stats as JSON")
	itemsIconCmd.Flags().Int64("itemdef", 0, "Steam itemdef id")
	itemsIconCmd.Flags().String("file", "", "Square PNG, JPEG, GIF, or WebP")
	itemsSchemaCmd.Flags().String("mode", "sync", "sync, overwrite, or addon")
	itemsGiveCmd.Flags().Int64("itemdef", 0, "Steam itemdef id")
	itemsGiveCmd.Flags().Int("quantity", 1, "How many to give each recipient")
	itemsGiveCmd.Flags().String("request", "", "Request id")
	itemsGiveCmd.Flags().StringArray("to", nil, "Handle or email, repeat for a list")
	itemsSetCmd.Flags().String("name", "", "Set name")
	itemsSetCmd.Flags().String("uid", "", "Set uid when changing an existing set")
	itemsSetCmd.Flags().String("pieces", "[]", "JSON list of itemdef ids")
	itemsSetCmd.Flags().String("bonus", "{}", "JSON bonus, stored and not applied")
	mintKeyCreateCmd.Flags().String("name", "", "Name shown next to the prefix")
	mintKeyRenameCmd.Flags().String("id", "", "Mint key uid")
	mintKeyRenameCmd.Flags().String("name", "", "New name")
	mintKeyRevokeCmd.Flags().String("id", "", "Mint key uid")
	_ = itemsCreateCmd.MarkFlagRequired("itemdef")
	_ = itemsCreateCmd.MarkFlagRequired("name")
	_ = itemsIconCmd.MarkFlagRequired("itemdef")
	_ = itemsIconCmd.MarkFlagRequired("file")
	_ = itemsGiveCmd.MarkFlagRequired("itemdef")
	_ = itemsGiveCmd.MarkFlagRequired("request")
	_ = itemsSetCmd.MarkFlagRequired("name")
	_ = mintKeyCreateCmd.MarkFlagRequired("name")
	_ = mintKeyRenameCmd.MarkFlagRequired("id")
	_ = mintKeyRenameCmd.MarkFlagRequired("name")
	_ = mintKeyRevokeCmd.MarkFlagRequired("id")
	itemsListCmd.Flags().String("cursor", "", "Page cursor from the previous next field")
	mintKeyListCmd.Flags().String("cursor", "", "Page cursor from the previous next field")
}

func pagedPath(path, cursor string) string {
	if cursor == "" {
		return path
	}
	return path + "?cursor=" + url.QueryEscape(cursor)
}

var itemsCmd = &cobra.Command{Use: "items", Short: "Create and edit item definitions, icons, Steam schema, and gifts"}
var mintKeyCmd = &cobra.Command{Use: "mint-key", Short: "Named 128-character keys that mint, set stats, and socket items"}

var itemsListCmd = &cobra.Command{
	Use: "list", Short: "List item definitions with minted and in-the-wild counts",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		cursor, _ := cmd.Flags().GetString("cursor")
		resp, err := client.GetJSON(pagedPath("/tool/items", cursor))
		if err != nil {
			return err
		}
		return emit(resp.Data)
	},
}

var itemsCreateCmd = &cobra.Command{
	Use: "create", Short: "Create an item definition",
	RunE: func(cmd *cobra.Command, args []string) error {
		return itemWrite(cmd, "POST")
	},
}

var itemsEditCmd = &cobra.Command{
	Use: "edit", Short: "Change an item definition. Unique stays as it was set",
	RunE: func(cmd *cobra.Command, args []string) error {
		return itemWrite(cmd, "PUT")
	},
}

func itemWrite(cmd *cobra.Command, method string) error {
	client, err := deployClient(cmd)
	if err != nil {
		return err
	}
	id, _ := cmd.Flags().GetInt64("itemdef")
	body := map[string]any{"itemdef_id": id}
	if name, _ := cmd.Flags().GetString("name"); name != "" {
		body["name"] = name
	}
	if t, _ := cmd.Flags().GetString("platform-type"); t != "" {
		body["platform_type"] = t
	}
	if cmd.Flags().Lookup("steam-type") != nil {
		if t, _ := cmd.Flags().GetString("steam-type"); t != "" {
			body["steam_type"] = t
		}
	}
	if stats, _ := cmd.Flags().GetString("stats"); stats != "" {
		body["base_stats"] = jsonRaw(stats)
	}
	if cmd.Flags().Lookup("stackable") != nil && cmd.Flags().Changed("stackable") {
		body["stackable"], _ = cmd.Flags().GetBool("stackable")
	}
	if cmd.Flags().Lookup("unique") != nil && cmd.Flags().Changed("unique") {
		body["unique"], _ = cmd.Flags().GetBool("unique")
	}
	if cmd.Flags().Lookup("socketable") != nil && cmd.Flags().Changed("socketable") {
		body["socketable"], _ = cmd.Flags().GetBool("socketable")
	}
	if cmd.Flags().Lookup("sockets") != nil && cmd.Flags().Changed("sockets") {
		body["socket_count"], _ = cmd.Flags().GetInt("sockets")
	}
	if cmd.Flags().Lookup("accepts") != nil {
		if accepts, _ := cmd.Flags().GetString("accepts"); accepts != "" && accepts != "[]" {
			body["socket_accepts"] = jsonRaw(accepts)
		}
	}
	if cmd.Flags().Lookup("tradable") != nil && cmd.Flags().Changed("tradable") {
		body["tradable"], _ = cmd.Flags().GetBool("tradable")
	}
	resp, err := client.sendJSON(method, "/tool/items", body)
	if err != nil {
		return err
	}
	return emit(resp.Data)
}

var itemsIconCmd = &cobra.Command{
	Use: "icon", Short: "Upload a square item icon to the CDN",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		id, _ := cmd.Flags().GetInt64("itemdef")
		path, _ := cmd.Flags().GetString("file")
		if _, err := os.Stat(path); err != nil {
			return usageErrorf("read %s: %v", path, err)
		}
		resp, _, err := client.PostMultipart(client.buildURL("/tool/items/icon"), []formField{{"itemdef_id", strconv.FormatInt(id, 10)}}, []formFile{{Field: "file", Path: path, Length: -1}}, nil)
		if err != nil {
			return err
		}
		return emit(resp.Data)
	},
}

var itemsSchemaCmd = &cobra.Command{
	Use: "schema", Short: "sync pulls then pushes, overwrite pushes, addon adds new defs",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		mode, _ := cmd.Flags().GetString("mode")
		resp, err := client.PostJSON("/tool/items/schema", map[string]any{"mode": mode})
		if err != nil {
			return err
		}
		return emit(resp.Data)
	},
}

var itemsGiveCmd = &cobra.Command{
	Use: "give", Short: "Give one item and quantity to one or more handles or emails",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		id, _ := cmd.Flags().GetInt64("itemdef")
		qty, _ := cmd.Flags().GetInt("quantity")
		request, _ := cmd.Flags().GetString("request")
		to, _ := cmd.Flags().GetStringArray("to")
		if len(to) == 0 {
			return usageErrorf("name at least one --to handle or email")
		}
		resp, err := client.PostJSON("/tool/items/give", map[string]any{
			"itemdef_id": id, "quantity": qty, "request_id": request, "recipients": to,
		})
		if err != nil {
			return err
		}
		return emit(resp.Data)
	},
}

var itemsSetCmd = &cobra.Command{
	Use: "set", Short: "Store a named set and its bonus. The game applies the bonus",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		name, _ := cmd.Flags().GetString("name")
		uid, _ := cmd.Flags().GetString("uid")
		pieces, _ := cmd.Flags().GetString("pieces")
		bonus, _ := cmd.Flags().GetString("bonus")
		resp, err := client.PostJSON("/tool/items/sets", map[string]any{
			"uid": uid, "name": name, "pieces": jsonRaw(pieces), "bonus": jsonRaw(bonus),
		})
		if err != nil {
			return err
		}
		return emit(resp.Data)
	},
}

var mintKeyListCmd = &cobra.Command{
	Use: "list", Short: "List mint key names and prefixes",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		cursor, _ := cmd.Flags().GetString("cursor")
		resp, err := client.GetJSON(pagedPath("/tool/mint-keys", cursor))
		if err != nil {
			return err
		}
		return emit(resp.Data)
	},
}

var mintKeyCreateCmd = &cobra.Command{
	Use: "create", Short: "Create a mint key. The secret is printed once",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		name, _ := cmd.Flags().GetString("name")
		resp, err := client.PostJSON("/tool/mint-keys", map[string]any{"name": name})
		if err != nil {
			return err
		}
		return emit(resp.Data)
	},
}

var mintKeyRenameCmd = &cobra.Command{
	Use: "rename", Short: "Rename a mint key",
	RunE: func(cmd *cobra.Command, args []string) error {
		id, _ := cmd.Flags().GetString("id")
		if _, err := mintKeyPath(id); err != nil {
			return err
		}
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		name, _ := cmd.Flags().GetString("name")
		resp, err := renameMintKey(client, id, name)
		if err != nil {
			return err
		}
		return emit(resp.Data)
	},
}

var mintKeyRevokeCmd = &cobra.Command{
	Use: "revoke", Short: "Revoke a mint key",
	RunE: func(cmd *cobra.Command, args []string) error {
		id, _ := cmd.Flags().GetString("id")
		if _, err := mintKeyPath(id); err != nil {
			return err
		}
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		resp, err := revokeMintKey(client, id)
		if err != nil {
			return err
		}
		return emit(resp.Data)
	},
}

func mintKeyPath(id string) (string, error) {
	id = strings.TrimSpace(id)
	if !validUID(id) {
		return "", usageErrorf("--id must be a mint key uid")
	}
	return "/tool/mint-keys/" + url.PathEscape(id), nil
}

func renameMintKey(c *Client, id, name string) (*APIResponse, error) {
	path, err := mintKeyPath(id)
	if err != nil {
		return nil, err
	}
	return c.PutJSON(path, map[string]any{"name": name})
}

func revokeMintKey(c *Client, id string) (*APIResponse, error) {
	path, err := mintKeyPath(id)
	if err != nil {
		return nil, err
	}
	return c.Delete(path)
}

func jsonRaw(s string) any {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	return v
}
