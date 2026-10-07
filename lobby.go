package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash/crc32"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

var lobbyDir string
var lobbyTypeFlag string
var lobbySetEnabled string
var lobbySetType string
var lobbySetIce string

var lobbyCmd = &cobra.Command{
	Use:   "lobby",
	Short: "Publish and inspect scripted lobby packs",
}

var lobbyPublishCmd = &cobra.Command{
	Use:   "publish --dir <path>",
	Short: "Pack every .lua file and upload it for this game",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		return runLobbyPublish(client, lobbyDir, lobbyTypeFlag)
	},
}

var lobbyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List lobby packs stored for this game",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		return runLobbyList(client)
	},
}

var lobbyStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the pack the next lobby will load",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		return runLobbyStatus(client)
	},
}

var lobbySetCmd = &cobra.Command{
	Use:   "set",
	Short: "Turn the lobby on, choose relay or scripted, and switch TURN",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := deployClient(cmd)
		if err != nil {
			return err
		}
		return runLobbySet(client, lobbySetEnabled, lobbySetType, lobbySetIce)
	},
}

func init() {
	lobbyPublishCmd.Flags().StringVar(&lobbyDir, "dir", "", "Directory of .lua files")
	lobbyPublishCmd.Flags().StringVar(&lobbyTypeFlag, "type", "", "Lobby type name")
	_ = lobbyPublishCmd.MarkFlagRequired("dir")
	lobbySetCmd.Flags().StringVar(&lobbySetEnabled, "enabled", "", "true or false")
	lobbySetCmd.Flags().StringVar(&lobbySetType, "type", "", "relay or scripted")
	lobbySetCmd.Flags().StringVar(&lobbySetIce, "ice", "", "true or false")
}

func runLobbyPublish(client *Client, dir, lobbyType string) error {
	files, err := lobbyLuaFiles(dir)
	if err != nil {
		return err
	}
	blob, err := storedZip(files)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(blob)
	checksum := hex.EncodeToString(sum[:])
	tmp, err := os.CreateTemp("", "lobby-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(blob); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	resp, _, err := client.PostMultipart(client.filesURL("/tool/lobby/publish"), []formField{
		{"checksum", checksum},
		{"lobby_type", lobbyType},
	}, []formFile{{Field: "file", Path: tmp.Name(), Length: -1}}, nil)
	if err != nil {
		return err
	}
	if jsonOutput {
		return emit(map[string]any{"ok": true, "checksum": checksum, "data": resp.Data})
	}
	logf("Published lobby pack %s\n", checksum)
	return nil
}

func runLobbyList(client *Client) error {
	resp, err := client.GetJSON("/tool/lobby")
	if err != nil {
		return err
	}
	if jsonOutput {
		return emit(resp.Data)
	}
	logf("%v\n", resp.Data)
	return nil
}

func lobbySetBody(enabled, lobbyType, ice string) (map[string]any, error) {
	body := map[string]any{}
	if enabled != "" {
		on, err := strconv.ParseBool(enabled)
		if err != nil {
			return nil, usageErrorf("--enabled must be true or false")
		}
		body["enabled"] = on
	}
	if lobbyType != "" {
		body["lobby_type"] = lobbyType
	}
	if ice != "" {
		on, err := strconv.ParseBool(ice)
		if err != nil {
			return nil, usageErrorf("--ice must be true or false")
		}
		body["ice_enabled"] = on
	}
	if len(body) == 0 {
		return nil, usageErrorf("pass --enabled, --type, or --ice")
	}
	return body, nil
}

func runLobbySet(client *Client, enabled, lobbyType, ice string) error {
	body, err := lobbySetBody(enabled, lobbyType, ice)
	if err != nil {
		return err
	}
	resp, err := client.PutJSON("/tool/lobby", body)
	if err != nil {
		return err
	}
	if jsonOutput {
		return emit(resp.Data)
	}
	logf("lobby enabled %v type %v ice %v\n", resp.Data["enabled"], resp.Data["lobby_type"], resp.Data["ice_enabled"])
	return nil
}

func runLobbyStatus(client *Client) error {
	resp, err := client.GetJSON("/tool/lobby/status")
	if err != nil {
		return err
	}
	if jsonOutput {
		return emit(resp.Data)
	}
	logf("active %v load %v matches %v\n", resp.Data["active_checksum"], resp.Data["load_status"], resp.Data["matches_latest"])
	return nil
}

func lobbyLuaFiles(dir string) ([]lobbyFile, error) {
	var files []lobbyFile
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".as" || ext != ".lua" {
			return usageErrorf("lobby packs are Luau only; refused %s", path)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files = append(files, lobbyFile{name: filepath.ToSlash(rel), body: body})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, usageErrorf("no .lua files under %s", dir)
	}
	return files, nil
}

type lobbyFile struct {
	name string
	body []byte
}

func storedZip(files []lobbyFile) ([]byte, error) {
	var body []byte
	var central []byte
	var offset uint32
	for _, file := range files {
		name := []byte(file.name)
		sum := crc32.ChecksumIEEE(file.body)
		local := make([]byte, 30+len(name))
		binary.LittleEndian.PutUint32(local[0:], 0x04034b50)
		binary.LittleEndian.PutUint16(local[4:], 20)
		binary.LittleEndian.PutUint32(local[14:], sum)
		binary.LittleEndian.PutUint32(local[18:], uint32(len(file.body)))
		binary.LittleEndian.PutUint32(local[22:], uint32(len(file.body)))
		binary.LittleEndian.PutUint16(local[26:], uint16(len(name)))
		copy(local[30:], name)
		body = append(body, local...)
		body = append(body, file.body...)
		entry := make([]byte, 46+len(name))
		binary.LittleEndian.PutUint32(entry[0:], 0x02014b50)
		binary.LittleEndian.PutUint16(entry[4:], 20)
		binary.LittleEndian.PutUint16(entry[6:], 20)
		binary.LittleEndian.PutUint32(entry[16:], sum)
		binary.LittleEndian.PutUint32(entry[20:], uint32(len(file.body)))
		binary.LittleEndian.PutUint32(entry[24:], uint32(len(file.body)))
		binary.LittleEndian.PutUint16(entry[28:], uint16(len(name)))
		binary.LittleEndian.PutUint32(entry[42:], offset)
		copy(entry[46:], name)
		central = append(central, entry...)
		offset += uint32(len(local) + len(file.body))
	}
	end := make([]byte, 22)
	binary.LittleEndian.PutUint32(end[0:], 0x06054b50)
	binary.LittleEndian.PutUint16(end[8:], uint16(len(files)))
	binary.LittleEndian.PutUint16(end[10:], uint16(len(files)))
	binary.LittleEndian.PutUint32(end[12:], uint32(len(central)))
	binary.LittleEndian.PutUint32(end[16:], offset)
	out := append(body, central...)
	out = append(out, end...)
	return out, nil
}
