package main

import (
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var progressFile string

var progressCmd = &cobra.Command{
	Use:   "progress",
	Short: "Show or replace achievements, stats, funnels, and presence writers",
}

var progressShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the current progress definitions",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		resp, err := client.GetJSON("/tool/progress")
		if err != nil {
			return err
		}
		if jsonOutput {
			return emit(resp.Data)
		}
		logf("%v\n", resp.Data)
		return nil
	},
}

var progressApplyCmd = &cobra.Command{
	Use:   "apply --file progress.yml",
	Short: "Replace each section that the file includes",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		return runProgressApply(client, progressFile)
	},
}

type progressFileDoc struct {
	Spec            string         `yaml:"spec"`
	Achievements    *[]progressDef `yaml:"achievements"`
	Stats           *[]progressDef `yaml:"stats"`
	Funnels         *[]progressDef `yaml:"funnels"`
	PresenceWriters *[]string      `yaml:"presence_writers"`
}

type progressDef struct {
	Slug    string   `yaml:"slug" json:"slug,omitempty"`
	Name    string   `yaml:"name" json:"name,omitempty"`
	Op      string   `yaml:"op" json:"op,omitempty"`
	Writers []string `yaml:"writers" json:"writers"`
}

func runProgressApply(client *Client, path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return usageErrorf("read %s: %v", path, err)
	}
	var doc progressFileDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return usageErrorf("progress file: %v", err)
	}
	if doc.Spec != "progress" {
		return usageErrorf("spec must be progress")
	}
	body := map[string]any{}
	if doc.Achievements != nil {
		body["achievements"] = *doc.Achievements
	}
	if doc.Stats != nil {
		body["stats"] = *doc.Stats
	}
	if doc.Funnels != nil {
		body["funnels"] = *doc.Funnels
	}
	if doc.PresenceWriters != nil {
		body["presence_writers"] = *doc.PresenceWriters
	}
	if len(body) == 0 {
		return usageErrorf("the file has no achievements, stats, funnels, or presence_writers section")
	}
	resp, err := client.PutJSON("/tool/progress", body)
	if err != nil {
		return err
	}
	if jsonOutput {
		return emit(resp.Data)
	}
	logln("Updated progress settings")
	return nil
}

func init() {
	progressApplyCmd.Flags().StringVar(&progressFile, "file", "", "Path to progress.yml")
	_ = progressApplyCmd.MarkFlagRequired("file")
	progressCmd.AddCommand(progressShowCmd, progressApplyCmd)
}
