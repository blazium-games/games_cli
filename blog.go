package main

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var blogSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// publicClient calls public routes. It does not send a deploy key.
func publicClient() (*Client, error) {
	raw := viper.GetString("url")
	if err := checkServiceURL(raw); err != nil {
		return nil, err
	}
	return NewClient(raw, "", ""), nil
}

var blogCmd = &cobra.Command{
	Use:   "blog",
	Short: "List published blog posts, newest first",
	Long: `List published posts from the Blazium Games blog, newest first.

This command calls the public articles API. It does not need a deploy key.`,
	Example: "  chauffeur blog\n  chauffeur blog --json",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := publicClient()
		if err != nil {
			return err
		}
		resp, err := c.GetJSON("/public/articles?limit=20")
		if err != nil {
			return err
		}
		articles, _ := resp.Data["articles"].([]any)
		if jsonOutput {
			return emit(map[string]any{"ok": true, "articles": articles})
		}
		if len(articles) == 0 {
			logln("No posts yet.")
			return nil
		}
		for i, item := range articles {
			m, _ := item.(map[string]any)
			if i > 0 {
				logln()
			}
			logf("%v\n", m["title"])
			if date := fmt.Sprint(m["date"]); date != "" && date != "<nil>" {
				logf("%s\n", date)
			}
			if desc := fmt.Sprint(m["description"]); desc != "" && desc != "<nil>" {
				logf("%s\n", desc)
			}
			logf("%v\n", m["url"])
		}
		return nil
	},
}

var blogShowCmd = &cobra.Command{
	Use:   "show <slug>",
	Short: "Print one blog post",
	Long: `Print the title and markdown of one published blog post.

This command calls the public articles API. It does not need a deploy key.`,
	Example: "  chauffeur blog show pardon-our-dust",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug := strings.TrimSpace(args[0])
		if !blogSlugPattern.MatchString(slug) || len(slug) > 80 {
			return usageErrorf("slug must be the post's kebab-case name")
		}
		c, err := publicClient()
		if err != nil {
			return err
		}
		resp, err := c.GetJSON("/public/articles/" + url.PathEscape(slug))
		if err != nil {
			return err
		}
		article, _ := resp.Data["article"].(map[string]any)
		if jsonOutput {
			return emit(map[string]any{"ok": true, "article": article})
		}
		logf("%v\n\n%s", article["title"], article["body"])
		return nil
	},
}
