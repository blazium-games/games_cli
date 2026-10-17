package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	bugsCmd.Flags().String("status", "", "Only this status: open, fixed, unidentified, or closed")
	bugsCmd.Flags().Int("page", 0, "One page only. 0 follows next_page until the list ends")
	shotCmd.Flags().String("out", "", "File to write")
	logCmd.Flags().String("out", "", "File to write")
	pullCmd.Flags().String("dir", ".", "Directory for the screenshot and the log")
	_ = shotCmd.MarkFlagRequired("out")
	_ = logCmd.MarkFlagRequired("out")
	bugsCmd.AddCommand(shotCmd, logCmd, pullCmd)
	rootCmd.AddCommand(bugsCmd)
}

var bugsCmd = &cobra.Command{
	Use:   "bugs",
	Short: "List bug reports for the deploy key's game",
	Long: `List bug reports filed for the game this deploy key belongs to.

The key can read tickets, screenshots, and logs. It cannot change status.
Screenshots and logs are saved with the screenshot, log, and pull commands.`,
	Example: "  chauffeur bugs\n  chauffeur bugs --status open\n  chauffeur bugs pull bug_123 --dir ./bugs",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := deployClient(cmd)
		if err != nil {
			return err
		}
		status, _ := cmd.Flags().GetString("status")
		page, _ := cmd.Flags().GetInt("page")
		bugs, err := listBugTickets(c, status, page)
		if err != nil {
			return err
		}
		if jsonOutput {
			return emit(map[string]any{"ok": true, "bugs": bugs})
		}
		if len(bugs) == 0 {
			logln("No bug reports.")
			return nil
		}
		for _, row := range bugs {
			logf("%s  %s  %s\n", str(row["uid"]), str(row["status"]), str(row["message"]))
		}
		return nil
	},
}

var shotCmd = &cobra.Command{
	Use:   "screenshot BUG_UID",
	Short: "Download a bug report's screenshot",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := deployClient(cmd)
		if err != nil {
			return err
		}
		out, _ := cmd.Flags().GetString("out")
		return saveBugScreenshot(c, args[0], out)
	},
}

var logCmd = &cobra.Command{
	Use:   "log BUG_UID",
	Short: "Download a bug report's full log",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := deployClient(cmd)
		if err != nil {
			return err
		}
		out, _ := cmd.Flags().GetString("out")
		body, err := readBugLog(c, args[0])
		if err != nil {
			return err
		}
		return os.WriteFile(out, body, 0o644)
	},
}

var pullCmd = &cobra.Command{
	Use:   "pull BUG_UID",
	Short: "Save a bug report's screenshot and log",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := deployClient(cmd)
		if err != nil {
			return err
		}
		dir, _ := cmd.Flags().GetString("dir")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		uid := args[0]
		resp, err := c.GetJSON("/tool/bugs/" + url.PathEscape(uid) + "/screenshot")
		if err == nil {
			if link := str(resp.Data["url"]); link != "" {
				if err := c.DownloadFile(link, filepath.Join(dir, uid+"-screenshot")); err != nil {
					return err
				}
			}
		} else if !missingBugFile(err) {
			return err
		}
		body, err := readBugLog(c, uid)
		if missingBugFile(err) || (err == nil && len(body) == 0) {
			return nil
		}
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, uid+"-log.txt"), body, 0o644)
	},
}

func listBugTickets(c *Client, status string, onlyPage int) ([]map[string]any, error) {
	page := 1
	if onlyPage > 0 {
		page = onlyPage
	}
	var all []map[string]any
	for {
		q := url.Values{}
		q.Set("page", strconv.Itoa(page))
		if s := strings.ToLower(strings.TrimSpace(status)); s != "" {
			q.Set("status", s)
		}
		resp, err := c.GetJSON("/tool/bugs?" + q.Encode())
		if err != nil {
			return nil, err
		}
		rows, _ := resp.Data["bugs"].([]any)
		for _, row := range rows {
			if m, ok := row.(map[string]any); ok {
				all = append(all, m)
			}
		}
		if onlyPage > 0 {
			return all, nil
		}
		if _, ok := resp.Data["next_page"]; !ok {
			return all, nil
		}
		page++
		if page > 1000 {
			return nil, fmt.Errorf("stopped after 1000 pages of bug reports")
		}
	}
}

func saveBugScreenshot(c *Client, uid, dest string) error {
	resp, err := c.GetJSON("/tool/bugs/" + url.PathEscape(uid) + "/screenshot")
	if err != nil {
		return err
	}
	link := str(resp.Data["url"])
	if link == "" {
		return fmt.Errorf("screenshot link missing")
	}
	return c.DownloadFile(link, dest)
}

func readBugLog(c *Client, uid string) ([]byte, error) {
	var buf strings.Builder
	offset := 0
	for {
		q := url.Values{}
		q.Set("offset", strconv.Itoa(offset))
		resp, err := c.GetJSON("/tool/bugs/" + url.PathEscape(uid) + "/log?" + q.Encode())
		if err != nil {
			return nil, err
		}
		buf.WriteString(str(resp.Data["text"]))
		next, ok := resp.Data["next_offset"]
		if !ok {
			return []byte(buf.String()), nil
		}
		n, err := strconv.Atoi(str(next))
		if err != nil || n <= offset {
			return []byte(buf.String()), nil
		}
		offset = n
	}
}

func missingBugFile(err error) bool {
	api, ok := err.(*apiError)
	return ok && (api.Status == 404 || api.Code == 4040)
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatInt(int64(t), 10)
	default:
		if v == nil {
			return ""
		}
		return fmt.Sprint(v)
	}
}
