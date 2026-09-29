package main

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

var gendocsCmd = &cobra.Command{
	Use:    "gendocs",
	Short:  "Write Markdown reference pages for every command",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, _ := cmd.Flags().GetString("dir")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return usageErrorf("cannot create %s: %v", dir, err)
		}
		rootCmd.DisableAutoGenTag = true
		if err := doc.GenMarkdownTree(rootCmd, dir); err != nil {
			return err
		}
		logf("Wrote command reference to %s\n", dir)
		return nil
	},
}

func init() {
	gendocsCmd.Flags().String("dir", "docs/commands", "Output directory")
}
