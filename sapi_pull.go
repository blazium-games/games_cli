package main

import (
	"archive/zip"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// chauffeur sapi pull downloads a stored Scripted API pack (a checksum, "active" or "latest") and unpacks it into a
// directory, so a developer or agent can edit the live scripts and publish them again. It never overwrites a file
// that differs from the pack unless --force is given.

const (
	sapiPullMaxFiles = 1000
	sapiPullMaxBytes = 64 << 20
)

var (
	sapiPullDir     string
	sapiPullChannel string
	sapiPullForce   bool
)

var sapiPullCmd = &cobra.Command{
	Use:   "pull [checksum|active|latest] --dir <path>",
	Short: "Download a stored pack (default: the one running on the channel) and unpack it for editing",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ref := "active"
		if len(args) == 1 {
			ref = strings.ToLower(strings.TrimSpace(args[0]))
		}
		if ref != "active" && ref != "latest" && !sapiChecksumRe.MatchString(ref) {
			return usageErrorf("the pack is a 64-character checksum from chauffeur sapi packs, or active or latest")
		}
		channel, err := sapiOptionalChannel(sapiPullChannel)
		if err != nil {
			return err
		}
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		endpoint := "/tool/sapi/packs/" + ref + "/download"
		if channel != "" {
			endpoint += "?channel=" + url.QueryEscape(channel)
		}
		resp, err := client.GetJSON(endpoint)
		if err != nil {
			return err
		}
		link, _ := resp.Data["url"].(string)
		if link == "" {
			return fmt.Errorf("the service did not return a download link")
		}
		tmp, err := os.CreateTemp("", "sapi-pack-*.zip")
		if err != nil {
			return err
		}
		tmp.Close()
		defer os.Remove(tmp.Name())
		if err := client.DownloadFile(link, tmp.Name()); err != nil {
			return err
		}
		written, err := extractSapiPack(tmp.Name(), sapiPullDir, sapiPullForce)
		if err != nil {
			return err
		}
		sum, _ := resp.Data["checksum"].(string)
		logf("Pulled pack %s into %s (%d files)\n", shortChecksum(sum), sapiPullDir, len(written))
		return emit(map[string]any{"ok": true, "checksum": sum, "dir": sapiPullDir, "files": written})
	},
}

func init() {
	sapiPullCmd.Flags().StringVar(&sapiPullDir, "dir", "", "Directory to unpack into")
	_ = sapiPullCmd.MarkFlagRequired("dir")
	sapiPullCmd.Flags().StringVar(&sapiPullChannel, "channel", "", "Release channel for active or latest (default stable)")
	sapiPullCmd.Flags().BoolVar(&sapiPullForce, "force", false, "Overwrite files that differ from the pack")
	sapiCmd.AddCommand(sapiPullCmd)
}

// extractSapiPack unpacks a pack zip into dir and returns the relative paths it wrote. Only .lua and .luau files are
// accepted, paths must stay inside dir, and a file that already differs is kept unless force is true.
func extractSapiPack(zipPath, dir string, force bool) ([]string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("the downloaded pack is not a readable zip: %w", err)
	}
	defer zr.Close()
	if len(zr.File) > sapiPullMaxFiles {
		return nil, fmt.Errorf("the pack has more than %d files", sapiPullMaxFiles)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	type planned struct {
		rel  string
		name string
		file *zip.File
	}
	var plan []planned
	var total uint64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rel := f.Name
		low := strings.ToLower(rel)
		if strings.HasPrefix(rel, "/") || strings.Contains(rel, "..") || strings.Contains(rel, `\`) || strings.Contains(rel, ":") ||
			(!strings.HasSuffix(low, ".luau") && !strings.HasSuffix(low, ".lua")) {
			return nil, fmt.Errorf("the pack has a file that is not allowed: %q", f.Name)
		}
		total += f.UncompressedSize64
		if total > sapiPullMaxBytes {
			return nil, fmt.Errorf("the pack is larger than %d MiB unpacked", sapiPullMaxBytes>>20)
		}
		name := filepath.FromSlash(rel)
		if !strings.HasPrefix(filepath.Join(abs, name), abs+string(os.PathSeparator)) {
			return nil, fmt.Errorf("the pack has a file outside the directory: %q", f.Name)
		}
		plan = append(plan, planned{rel: rel, name: name, file: f})
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	body := make(map[string][]byte, len(plan))
	var conflicts []string
	for _, p := range plan {
		rc, err := p.file.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(rc, int64(p.file.UncompressedSize64)+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		body[p.rel] = data
		if !force {
			if cur, rerr := root.ReadFile(p.name); rerr == nil && string(cur) != string(data) {
				conflicts = append(conflicts, p.rel)
			}
		}
	}
	if len(conflicts) > 0 {
		return nil, usageErrorf("%d local file(s) differ from the pack and were left alone (%s). Use --force to overwrite them", len(conflicts), strings.Join(conflicts, ", "))
	}
	var written []string
	for _, p := range plan {
		if err := writeFileInRoot(root, p.name, body[p.rel], 0o644); err != nil {
			return nil, err
		}
		written = append(written, p.rel)
	}
	return written, nil
}
