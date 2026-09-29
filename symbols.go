package main

import (
	"archive/zip"
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// symbolUpload is a prepared symbols payload: one .sym or one .zip of .sym files.
type symbolUpload struct {
	Path    string
	Count   int
	Size    int64
	cleanup func()
}

// prepareSymbols turns a .sym file, a directory of .sym files, or a .zip into
// a single file ready for /tool/upload/symbols, checked against the service limits.
func prepareSymbols(path string) (*symbolUpload, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, usageErrorf("cannot read %s: %v", path, err)
	}
	if st.IsDir() {
		return zipSymbolDir(path)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".sym":
		if st.Size() > maxSymbolFileBytes {
			return nil, usageErrorf("%s is %s; a symbol file can be at most 512 MB", path, sizeText(st.Size()))
		}
		if err := checkSymHeader(path); err != nil {
			return nil, err
		}
		return &symbolUpload{Path: path, Count: 1, Size: st.Size(), cleanup: func() {}}, nil
	case ".zip":
		n, err := checkSymbolZip(path, st.Size())
		if err != nil {
			return nil, err
		}
		return &symbolUpload{Path: path, Count: n, Size: st.Size(), cleanup: func() {}}, nil
	}
	return nil, usageErrorf("%s must be a .sym file, a .zip of .sym files, or a directory", path)
}

// checkSymHeader requires the Breakpad "MODULE <os> <arch> <id> <name>" first line.
func checkSymHeader(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return usageErrorf("cannot open %s: %v", path, err)
	}
	defer f.Close()
	line, _ := bufio.NewReader(f).ReadString('\n')
	if parts := strings.Fields(line); len(parts) < 5 || parts[0] != "MODULE" {
		return usageErrorf("%s is not a Breakpad symbol file (the first line must be MODULE <os> <arch> <id> <name>)", path)
	}
	return nil
}

func checkSymbolZip(path string, size int64) (int, error) {
	if size > maxSymbolUploadBytes {
		return 0, usageErrorf("%s is %s; a symbols upload can be at most 1 GB", path, sizeText(size))
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return 0, usageErrorf("%s is not a readable zip: %v", path, err)
	}
	defer zr.Close()
	var count int
	var total uint64
	for _, e := range zr.File {
		if e.FileInfo().IsDir() || !strings.EqualFold(filepath.Ext(e.Name), ".sym") {
			continue
		}
		count++
		total += e.UncompressedSize64
		switch {
		case count > maxSymbolsPerUpload:
			return 0, usageErrorf("%s has more than %d .sym files; split it", path, maxSymbolsPerUpload)
		case e.UncompressedSize64 > maxSymbolFileBytes:
			return 0, usageErrorf("%s in %s is larger than 512 MB", e.Name, path)
		case total > maxSymbolZipBytes:
			return 0, usageErrorf("the .sym files in %s add up to more than 2 GB", path)
		}
	}
	if count == 0 {
		return 0, usageErrorf("%s has no .sym files", path)
	}
	return count, nil
}

func zipSymbolDir(dir string) (*symbolUpload, error) {
	var files []string
	var total int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(p), ".sym") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxSymbolFileBytes {
			return usageErrorf("%s is %s; a symbol file can be at most 512 MB", p, sizeText(info.Size()))
		}
		if err := checkSymHeader(p); err != nil {
			return err
		}
		total += info.Size()
		files = append(files, p)
		return nil
	})
	if err != nil {
		if exitCode(err) == exitUsage {
			return nil, err
		}
		return nil, usageErrorf("cannot scan %s: %v", dir, err)
	}
	switch {
	case len(files) == 0:
		return nil, usageErrorf("%s has no .sym files", dir)
	case len(files) > maxSymbolsPerUpload:
		return nil, usageErrorf("%s has %d .sym files; upload at most %d at a time", dir, len(files), maxSymbolsPerUpload)
	case total > maxSymbolZipBytes:
		return nil, usageErrorf("the .sym files in %s add up to %s; the limit is 2 GB", dir, sizeText(total))
	}
	tmp, err := os.CreateTemp("", "chauffeur-symbols-*.zip")
	if err != nil {
		return nil, err
	}
	tmp.Close()
	cleanup := func() { os.Remove(tmp.Name()) }
	if err := CreateZip(tmp.Name(), files); err != nil {
		cleanup()
		return nil, usageErrorf("cannot zip %s: %v", dir, err)
	}
	st, err := os.Stat(tmp.Name())
	if err != nil {
		cleanup()
		return nil, err
	}
	if st.Size() > maxSymbolUploadBytes {
		cleanup()
		return nil, usageErrorf("the zipped symbols are %s; a symbols upload can be at most 1 GB", sizeText(st.Size()))
	}
	return &symbolUpload{Path: tmp.Name(), Count: len(files), Size: st.Size(), cleanup: cleanup}, nil
}

// uploadSymbols sends Breakpad symbols for buildID and returns the service data.
func uploadSymbols(c *Client, buildID, path string) (map[string]any, error) {
	buildID = strings.ToLower(strings.TrimSpace(buildID))
	if !validUID(buildID) {
		return nil, usageErrorf("--build-id must be a build uid (printed by chauffeur build)")
	}
	up, err := prepareSymbols(path)
	if err != nil {
		return nil, err
	}
	defer up.cleanup()
	sum, err := CalculateSHA256(up.Path)
	if err != nil {
		return nil, err
	}
	logf("Uploading %d symbol file(s) (%s) for build %s...\n", up.Count, sizeText(up.Size), buildID)
	name := filepath.Base(up.Path)
	if strings.EqualFold(filepath.Ext(up.Path), ".zip") {
		name = "symbols.zip"
	}
	return withRetry("symbols upload", func() (map[string]any, error) {
		resp, _, err := c.PostMultipart(c.filesURL("/tool/upload/symbols"),
			[]formField{{"build_id", buildID}, {"checksum", sum}},
			[]formFile{{Field: "file", Path: up.Path, Name: name, Length: -1}}, nil)
		if err != nil {
			return nil, err
		}
		return resp.Data, nil
	})
}

var symbolsCmd = &cobra.Command{
	Use:   "symbols --build-id <build-id> <file.sym|symbols-dir|symbols.zip>",
	Short: "Upload Breakpad symbols for a build",
	Long: `Upload Breakpad .sym files so crash reports for a build are symbolicated.

The path can be one .sym file, a directory (searched recursively for .sym
files, which are zipped for you), or a .zip of .sym files. Limits: 512 MB per
.sym file, 200 files and 2 GB uncompressed per upload, 1 GB upload size. Every
.sym file must start with a Breakpad MODULE line.

Symbols are private: only the game's developers can list or delete them, on
the web or through MCP. Uploading is only possible here, with the game's
deploy key (BLAZIUM_ACCESS_TOKEN and BLAZIUM_SECRET_KEY).`,
	Example: `  chauffeur symbols --build-id 5f1c2d3e-0000-4000-8000-000000000000 build/game.sym
  chauffeur symbols --build-id "$BLAZIUM_GAMES_BUILD_ID" build/symbols/`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		buildID, _ := cmd.Flags().GetString("build-id")
		c, err := deployClient(cmd)
		if err != nil {
			return err
		}
		data, err := uploadSymbols(c, buildID, args[0])
		if err != nil {
			return err
		}
		n := int64Of(data["count"])
		logf("Uploaded %d symbol file(s).\n", n)
		if jsonOutput {
			return emit(map[string]any{"ok": true, "build_id": data["build_id"], "count": n, "symbols": data["symbols"]})
		}
		return nil
	},
}

func init() {
	symbolsCmd.Flags().String("build-id", "", "Build uid the symbols belong to (required)")
	_ = symbolsCmd.MarkFlagRequired("build-id")
}
