package main

import (
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	_ "golang.org/x/image/webp"
)

var imageTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

// checkImage validates one image the way games_service does: type sniffed from
// the bytes (not the extension), at most 10 MB, 512-2048 px per side.
func checkImage(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return usageErrorf("cannot open image %s: %v", path, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return usageErrorf("cannot read image %s: %v", path, err)
	}
	if st.IsDir() {
		return usageErrorf("%s is a directory, not an image", path)
	}
	if st.Size() > maxImageBytes {
		return usageErrorf("%s is %s; images can be at most 10 MB", path, sizeText(st.Size()))
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	ctype := http.DetectContentType(head[:n])
	if !imageTypes[ctype] {
		return usageErrorf("%s is %s; use PNG, JPEG, GIF or WebP", path, ctype)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return usageErrorf("cannot read image %s: %v", path, err)
	}
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return usageErrorf("%s could not be decoded as an image: %v", path, err)
	}
	if cfg.Width < minImageSide || cfg.Height < minImageSide || cfg.Width > maxImageSide || cfg.Height > maxImageSide {
		return usageErrorf("%s is %dx%d px; each side must be %d-%d px", path, cfg.Width, cfg.Height, minImageSide, maxImageSide)
	}
	return nil
}

// mediaState is GET /tool/media.
type mediaState struct {
	GameUID    string         `json:"game_uid"`
	Cover      string         `json:"cover"`
	Thumbnail  string         `json:"thumbnail"`
	Gallery    []galleryImage `json:"gallery"`
	GalleryMax int            `json:"gallery_max"`
}

type galleryImage struct {
	UID      string `json:"uid"`
	URL      string `json:"url"`
	Position int    `json:"position"`
}

func parseMedia(data map[string]any) mediaState {
	m := mediaState{GalleryMax: maxGalleryImages}
	m.GameUID, _ = data["game_uid"].(string)
	m.Cover, _ = data["cover"].(string)
	m.Thumbnail, _ = data["thumbnail"].(string)
	if n := int(int64Of(data["gallery_max"])); n > 0 {
		m.GalleryMax = n
	}
	items, _ := data["gallery"].([]any)
	for _, it := range items {
		g, ok := it.(map[string]any)
		if !ok {
			continue
		}
		uid, _ := g["uid"].(string)
		u, _ := g["url"].(string)
		m.Gallery = append(m.Gallery, galleryImage{UID: uid, URL: u, Position: int(int64Of(g["position"]))})
	}
	return m
}

func (m mediaState) uids() []string {
	out := make([]string, len(m.Gallery))
	for i, g := range m.Gallery {
		out[i] = g.UID
	}
	return out
}

func getMedia(c *Client) (mediaState, error) {
	resp, err := c.GetJSON("/tool/media")
	if err != nil {
		return mediaState{}, err
	}
	return parseMedia(resp.Data), nil
}

// addMedia uploads a cover, a thumbnail, or 1-10 gallery images. position < 0
// appends gallery images.
func addMedia(c *Client, kind string, paths []string, position int) (mediaState, error) {
	switch kind {
	case "cover", "thumbnail":
		if len(paths) != 1 {
			return mediaState{}, usageErrorf("%s takes exactly one image", kind)
		}
	case "gallery":
		if len(paths) == 0 || len(paths) > maxGalleryPerRequest {
			return mediaState{}, usageErrorf("add 1-%d gallery images at a time", maxGalleryPerRequest)
		}
	default:
		return mediaState{}, usageErrorf("kind must be cover, thumbnail or gallery")
	}
	if position >= maxGalleryImages {
		return mediaState{}, usageErrorf("--position must be 0-%d", maxGalleryImages-1)
	}
	for _, p := range paths {
		if err := checkImage(p); err != nil {
			return mediaState{}, err
		}
	}
	fields := []formField{{"kind", kind}}
	if position >= 0 && kind == "gallery" {
		fields = append(fields, formField{"position", strconv.Itoa(position)})
	}
	files := make([]formFile, len(paths))
	for i, p := range paths {
		files[i] = formFile{Field: "file", Path: p, Length: -1}
	}
	resp, _, err := c.PostMultipart(c.buildURL("/tool/media"), fields, files, nil)
	if err != nil {
		return mediaState{}, err
	}
	return parseMedia(resp.Data), nil
}

// uploadMediaSpec applies a build.yml media section and legacy images list.
func uploadMediaSpec(c *Client, m *MediaSpec, legacyImages []string) (*mediaState, error) {
	var gallery []string
	gallery = append(gallery, legacyImages...)
	if m != nil {
		gallery = append(gallery, m.Gallery...)
	}
	var last *mediaState
	if m != nil {
		for _, kv := range [][2]string{{"cover", m.Cover}, {"thumbnail", m.Thumbnail}} {
			if kv[1] == "" {
				continue
			}
			logf("Uploading %s %s...\n", kv[0], kv[1])
			st, err := addMedia(c, kv[0], []string{kv[1]}, -1)
			if err != nil {
				return nil, err
			}
			last = &st
		}
	}
	for start := 0; start < len(gallery); start += maxGalleryPerRequest {
		batch := gallery[start:min(start+maxGalleryPerRequest, len(gallery))]
		logf("Uploading %d gallery image(s)...\n", len(batch))
		st, err := addMedia(c, "gallery", batch, -1)
		if err != nil {
			return nil, err
		}
		last = &st
	}
	return last, nil
}

// reorderMove moves uid to index to within order.
func reorderMove(order []string, uid string, to int) ([]string, error) {
	i := slices.Index(order, uid)
	if i < 0 {
		return nil, usageErrorf("%s is not in the gallery (see chauffeur media list)", uid)
	}
	if to < 0 || to >= len(order) {
		return nil, usageErrorf("--to must be 0-%d", len(order)-1)
	}
	out := slices.Delete(slices.Clone(order), i, i+1)
	return slices.Insert(out, to, uid), nil
}

// validateOrder checks that order is an exact permutation of current.
func validateOrder(order, current []string) error {
	if len(order) != len(current) {
		return usageErrorf("give all %d gallery image uids; got %d", len(current), len(order))
	}
	seen := map[string]bool{}
	for _, u := range order {
		if !slices.Contains(current, u) {
			return usageErrorf("%s is not in the gallery", u)
		}
		if seen[u] {
			return usageErrorf("%s is listed twice", u)
		}
		seen[u] = true
	}
	return nil
}

func printMedia(m mediaState) {
	show := func(v string) string {
		if v == "" {
			return "(none)"
		}
		return v
	}
	logf("Cover:     %s\n", show(m.Cover))
	logf("Thumbnail: %s\n", show(m.Thumbnail))
	logf("Gallery:   %d of %d\n", len(m.Gallery), m.GalleryMax)
	for _, g := range m.Gallery {
		logf("  %2d  %s  %s\n", g.Position, g.UID, g.URL)
	}
}

func mediaResult(action string, m mediaState) error {
	if !jsonOutput {
		printMedia(m)
		return nil
	}
	return emit(map[string]any{"ok": true, "action": action, "media": m})
}

const mediaAuthNote = `Authenticates with the game's deploy key: BLAZIUM_ACCESS_TOKEN and
BLAZIUM_SECRET_KEY (or --secret-stdin).`

var mediaCmd = &cobra.Command{
	Use:   "media",
	Short: "Manage store page images (cover, thumbnail, gallery)",
	Long: `Manage the store page images of the game that owns the deploy key.

Images must be PNG, JPEG, GIF or WebP, 512-2048 px per side and at most 10 MB.
The type is checked from the file contents, not the extension. The gallery
holds up to 20 images; a public listing needs at least 4, a cover and a
thumbnail.

` + mediaAuthNote,
	Example: `  chauffeur media list
  chauffeur media cover art/cover.png
  chauffeur media add shots/*.png --position 0
  chauffeur media move 5f1c...-uid --to 2`,
}

var mediaListCmd = &cobra.Command{
	Use:     "list",
	Short:   "Show the cover, thumbnail and gallery with image uids",
	Args:    cobra.NoArgs,
	Example: "  chauffeur media list\n  chauffeur media list --json",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := deployClient(cmd)
		if err != nil {
			return err
		}
		m, err := getMedia(c)
		if err != nil {
			return err
		}
		return mediaResult("list", m)
	},
}

func singleImageCmd(kind string) *cobra.Command {
	return &cobra.Command{
		Use:     kind + " <image>",
		Short:   "Replace the " + kind + " image",
		Long:    "Upload one image as the " + kind + ", replacing the current one.\n\n" + mediaAuthNote,
		Args:    cobra.ExactArgs(1),
		Example: "  chauffeur media " + kind + " art/" + kind + ".png",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := deployClient(cmd)
			if err != nil {
				return err
			}
			m, err := addMedia(c, kind, args, -1)
			if err != nil {
				return err
			}
			return mediaResult(kind, m)
		},
	}
}

var mediaAddCmd = &cobra.Command{
	Use:   "add <image>...",
	Short: "Add 1-10 gallery images",
	Long: `Add gallery images. Without --position they are appended; with it they are
inserted at that index (0 is first) and the rest shift down.

` + mediaAuthNote,
	Args:    cobra.RangeArgs(1, maxGalleryPerRequest),
	Example: "  chauffeur media add shot1.png shot2.png\n  chauffeur media add hero.png --position 0",
	RunE: func(cmd *cobra.Command, args []string) error {
		pos, _ := cmd.Flags().GetInt("position")
		c, err := deployClient(cmd)
		if err != nil {
			return err
		}
		m, err := addMedia(c, "gallery", args, pos)
		if err != nil {
			return err
		}
		return mediaResult("add", m)
	},
}

var mediaDeleteCmd = &cobra.Command{
	Use:   "delete <image-uid|cover|thumbnail>",
	Short: "Delete a gallery image, or clear the cover or thumbnail",
	Long: `Delete one gallery image by uid (see chauffeur media list), or clear the cover
or thumbnail. A public listing keeps its cover and thumbnail: replace them
instead.

` + mediaAuthNote,
	Args:    cobra.ExactArgs(1),
	Example: "  chauffeur media delete 5f1c2d3e-0000-4000-8000-000000000000\n  chauffeur media delete thumbnail",
	RunE: func(cmd *cobra.Command, args []string) error {
		target := strings.ToLower(strings.TrimSpace(args[0]))
		if target != "cover" && target != "thumbnail" && !validUID(target) {
			return usageErrorf("give a gallery image uid, cover or thumbnail")
		}
		c, err := deployClient(cmd)
		if err != nil {
			return err
		}
		resp, err := c.Delete("/tool/media/" + url.PathEscape(target))
		if err != nil {
			return err
		}
		return mediaResult("delete", parseMedia(resp.Data))
	},
}

var mediaMoveCmd = &cobra.Command{
	Use:     "move <image-uid> --to N",
	Short:   "Move one gallery image to a new position",
	Long:    "Move one gallery image to index N (0 is first); the others keep their order.\n\n" + mediaAuthNote,
	Args:    cobra.ExactArgs(1),
	Example: "  chauffeur media move 5f1c2d3e-0000-4000-8000-000000000000 --to 0",
	RunE: func(cmd *cobra.Command, args []string) error {
		to, _ := cmd.Flags().GetInt("to")
		uid := strings.ToLower(strings.TrimSpace(args[0]))
		if !validUID(uid) {
			return usageErrorf("give a gallery image uid (see chauffeur media list)")
		}
		c, err := deployClient(cmd)
		if err != nil {
			return err
		}
		m, err := getMedia(c)
		if err != nil {
			return err
		}
		order, err := reorderMove(m.uids(), uid, to)
		if err != nil {
			return err
		}
		resp, err := c.PutJSON("/tool/media/order", map[string]any{"order": order})
		if err != nil {
			return err
		}
		return mediaResult("move", parseMedia(resp.Data))
	},
}

var mediaOrderCmd = &cobra.Command{
	Use:     "order <image-uid>...",
	Short:   "Set the full gallery order",
	Long:    "Set the gallery order. List every gallery image uid exactly once.\n\n" + mediaAuthNote,
	Args:    cobra.MinimumNArgs(1),
	Example: "  chauffeur media order uid-3 uid-1 uid-2",
	RunE: func(cmd *cobra.Command, args []string) error {
		order := make([]string, len(args))
		for i, a := range args {
			order[i] = strings.ToLower(strings.TrimSpace(a))
			if !validUID(order[i]) {
				return usageErrorf("%s is not an image uid", a)
			}
		}
		c, err := deployClient(cmd)
		if err != nil {
			return err
		}
		m, err := getMedia(c)
		if err != nil {
			return err
		}
		if err := validateOrder(order, m.uids()); err != nil {
			return err
		}
		resp, err := c.PutJSON("/tool/media/order", map[string]any{"order": order})
		if err != nil {
			return err
		}
		return mediaResult("order", parseMedia(resp.Data))
	},
}

func init() {
	mediaAddCmd.Flags().Int("position", -1, fmt.Sprintf("Insert at this gallery index (0-%d); default appends", maxGalleryImages-1))
	mediaMoveCmd.Flags().Int("to", -1, "New gallery index (0 is first)")
	_ = mediaMoveCmd.MarkFlagRequired("to")
	mediaCmd.AddCommand(mediaListCmd, singleImageCmd("cover"), singleImageCmd("thumbnail"), mediaAddCmd, mediaDeleteCmd, mediaMoveCmd, mediaOrderCmd)
}

// imageFiles expands directories into the images inside them, sorted by name.
func imageFiles(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, usageErrorf("cannot read %s: %v", p, err)
		}
		if !st.IsDir() {
			out = append(out, p)
			continue
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil, usageErrorf("cannot read %s: %v", p, err)
		}
		for _, e := range entries {
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if !e.IsDir() && (ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".webp") {
				out = append(out, filepath.Join(p, e.Name()))
			}
		}
	}
	return out, nil
}