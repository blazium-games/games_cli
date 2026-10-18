package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	patronCmd.PersistentFlags().String("user-token", "", "Account access token (overrides BLAZIUM_USER_TOKEN). This is not a deploy key.")
	patronStatusCmd.Flags().String("developer", "", "Developer username. Empty lists every membership")
	rootCmd.AddCommand(patronCmd)
	patronCmd.AddCommand(patronStatusCmd, patronListCmd)
}

var patronCmd = &cobra.Command{Use: "patron", Short: "Patron memberships for the signed-in account"}

var patronStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the signed-in account's patron memberships",
	RunE: func(cmd *cobra.Command, _ []string) error {
		developer, _ := cmd.Flags().GetString("developer")
		path := "/private/patron/status"
		if developer != "" {
			path += "?developer=" + developer
		}
		return printUserAPI(cmd, path)
	},
}

var patronListCmd = &cobra.Command{
	Use:   "list",
	Short: "List patrons of the signed-in developer",
	RunE: func(cmd *cobra.Command, _ []string) error {
		return printUserAPI(cmd, "/private/patrons")
	},
}

func printUserAPI(cmd *cobra.Command, path string) error {
	token, _ := cmd.Flags().GetString("user-token")
	if strings.TrimSpace(token) == "" {
		token = os.Getenv("BLAZIUM_USER_TOKEN")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return usageErrorf("a user access token is required: set BLAZIUM_USER_TOKEN or pass --user-token. A deploy key cannot call this")
	}
	base := strings.TrimRight(apiURL, "/")
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return fmt.Errorf("patron request failed: %s", strings.TrimSpace(string(body)))
	}
	if jsonOutput {
		fmt.Println(string(body))
		return nil
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if json.Unmarshal(body, &env) == nil && env.Data != nil {
		out, _ := json.MarshalIndent(env.Data, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	fmt.Println(string(body))
	return nil
}
