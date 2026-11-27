package main

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSapiActivateBody(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	body, err := sapiActivateBody(strings.ToUpper(sum), "stable")
	if err != nil || body["checksum"] != sum || body["channel"] != "stable" {
		t.Fatalf("%v %v", body, err)
	}
	body, err = sapiActivateBody("none", "beta")
	if err != nil || body["checksum"] != "" || body["channel"] != "beta" {
		t.Fatalf("%v %v", body, err)
	}
	for _, bad := range []string{"abc", "../x", sum + "0"} {
		if _, err := sapiActivateBody(bad, "stable"); err == nil {
			t.Fatalf("accepted checksum %q", bad)
		}
	}
	if _, err := sapiActivateBody(sum, "Bad Channel"); err == nil {
		t.Fatal("accepted a bad channel")
	}
	if _, err := sapiActivateBody(sum, ""); err == nil {
		t.Fatal("activated without a channel: there is no default channel")
	}
}

func TestSapiChannelIsRequiredForPublishAndDev(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.luau"), []byte("return {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Nothing is sent: the missing channel is refused before the client is used.
	if err := runSapiPublish(testClient("http://127.0.0.1:1"), dir, "", true); err == nil {
		t.Fatal("publish ran without a channel")
	}
	if _, err := prepareSapiDev(dir, "free", ""); err == nil {
		t.Fatal("dev ran without a channel")
	}
	if _, err := prepareSapiDev(dir, "free", "Bad Channel"); err == nil {
		t.Fatal("dev accepted a bad channel")
	}
	if _, err := prepareSapiDev(dir, "gold", "beta"); err == nil {
		t.Fatal("dev accepted an unknown plan")
	}
	ch, err := prepareSapiDev(dir, "premium", " Beta ")
	if err != nil || ch != "beta" {
		t.Fatalf("%q %v", ch, err)
	}
}

func TestSapiDevPublishesAndActivatesTheChannel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.luau"), []byte("return {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var fields map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tool/sapi/features" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"managed": false, "features": []any{}}})
			return
		}
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		fields = map[string]string{}
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			raw, _ := io.ReadAll(part)
			if part.FormName() != "file" {
				fields[part.FormName()] = string(raw)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"ok": true, "valid": true, "activated": true}})
	}))
	defer srv.Close()
	ch, err := prepareSapiDev(dir, "free", "beta")
	if err != nil {
		t.Fatal(err)
	}
	if err := runSapiPublish(testClient(srv.URL), dir, ch, true); err != nil {
		t.Fatal(err)
	}
	if fields["channel"] != "beta" || fields["activate"] != "true" {
		t.Fatalf("%v", fields)
	}
}

func TestSapiSliceScopes(t *testing.T) {
	s, err := newSapiSlice("channel", "", "")
	if err != nil || s.channel != "stable" {
		t.Fatalf("%+v %v", s, err)
	}
	if got := s.listPath("save/", "", 500); got != "/tool/sapi/data?channel=stable&limit=200&prefix=save%2F&scope=channel" {
		t.Fatal(got)
	}
	if _, err := newSapiSlice("user", "", ""); err == nil {
		t.Fatal("user scope without --user")
	}
	if _, err := newSapiSlice("global", "beta", ""); err == nil {
		t.Fatal("global scope with a channel")
	}
	if _, err := newSapiSlice("table", "", ""); err == nil {
		t.Fatal("unknown scope")
	}
	u, err := newSapiSlice("user", "beta", "u-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := u.path("/tool/sapi/data/key", map[string][]string{"key": {"a b"}}); got != "/tool/sapi/data/key?channel=beta&key=a+b&scope=user&user=u-1" {
		t.Fatal(got)
	}
}

func TestSapiValueAndConfig(t *testing.T) {
	if v := parseSapiValue(`{"a":1}`); v.(map[string]any)["a"] != float64(1) {
		t.Fatalf("%v", v)
	}
	if v := parseSapiValue("hello"); v != "hello" {
		t.Fatalf("%v", v)
	}
	body, err := sapiConfigBody("season", "3", "beta", true)
	if err != nil || body["value"] != float64(3) || body["public"] != true || body["channel"] != "beta" {
		t.Fatalf("%v %v", body, err)
	}
	if _, err := sapiConfigBody("bad name", "1", "", false); err == nil {
		t.Fatal("accepted a bad name")
	}
	path, err := sapiNamedPath("/tool/sapi/secrets", "TOKEN", "")
	if err != nil || path != "/tool/sapi/secrets?name=TOKEN" {
		t.Fatalf("%q %v", path, err)
	}
}

func TestSapiLogsPath(t *testing.T) {
	path, err := sapiLogsPath("submit_score", "beta", "error", "", "", "", "", 5000)
	if err != nil || path != "/tool/sapi/logs?channel=beta&level=error&limit=1000&logic=submit_score" {
		t.Fatalf("%q %v", path, err)
	}
	if _, err := sapiLogsPath("", "", "loud", "", "", "", "", 0); err == nil {
		t.Fatal("accepted a bad level")
	}
	if _, err := sapiLogsPath("../x", "", "", "", "", "", "", 0); err == nil {
		t.Fatal("accepted a bad logic")
	}
}

func TestSapiLogsPathTimesAndCursor(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	saved := sapiNow
	sapiNow = func() time.Time { return now }
	defer func() { sapiNow = saved }()

	path, err := sapiLogsPath("", "", "", "", "30m", "2026-01-01T11:59:00Z", "4503599627370497", 0)
	want := "/tool/sapi/logs?cursor=4503599627370497&since=" + strconv.FormatInt(now.Add(-30*time.Minute).UnixMilli(), 10) + "&until=2026-01-01T11%3A59%3A00Z"
	if err != nil || path != want {
		t.Fatalf("%q %v", path, err)
	}
	path, err = sapiLogsPath("", "", "", "", "1767225600000", "7d", "", 0)
	if err != nil || path != "/tool/sapi/logs?since=1767225600000&until="+strconv.FormatInt(now.Add(-7*24*time.Hour).UnixMilli(), 10) {
		t.Fatalf("%q %v", path, err)
	}
	for _, bad := range [][2]string{{"since", "yesterday"}, {"since", "0m"}, {"until", "2026-01-01"}, {"until", "5w"}} {
		args := map[string]string{bad[0]: bad[1]}
		if _, err := sapiLogsPath("", "", "", "", args["since"], args["until"], "", 0); err == nil {
			t.Fatalf("accepted --%s %q", bad[0], bad[1])
		}
	}
	for _, bad := range []string{"abc", "-1", "0", "1 2", "99999999999999999999"} {
		if _, err := sapiLogsPath("", "", "", "", "", "", bad, 0); err == nil {
			t.Fatalf("accepted cursor %q", bad)
		}
	}
}

func TestSapiSecretValue(t *testing.T) {
	v, err := readSapiSecretValue("", strings.NewReader("s3cret\r\n"))
	if err != nil || v != "s3cret" {
		t.Fatalf("%q %v", v, err)
	}
	if _, err := readSapiSecretValue("", strings.NewReader("\n")); err == nil {
		t.Fatal("accepted an empty secret")
	}
	if _, err := readSapiSecretValue("", strings.NewReader(strings.Repeat("x", 16<<10+1))); err == nil {
		t.Fatal("accepted an oversized secret")
	}
}

func TestSapiPackCapsPerPlan(t *testing.T) {
	files := make([]lobbyFile, 21)
	for i := range files {
		files[i] = lobbyFile{name: "f.luau", body: []byte("return 1\n")}
	}
	if _, err := checkSapiPack(files, "free"); err == nil {
		t.Fatal("free allowed 21 files")
	}
	if _, err := checkSapiPack(files, "premium"); err != nil {
		t.Fatal(err)
	}
	big := []lobbyFile{{name: "big.luau", body: make([]byte, 256<<10+1)}}
	if _, err := checkSapiPack(big, "free"); err == nil {
		t.Fatal("free allowed an oversized pack")
	}
	if p, err := sapiPlanName("Grow"); err != nil || p != "premium" {
		t.Fatalf("%q %v", p, err)
	}
	if _, err := sapiPlanName("gold"); err == nil {
		t.Fatal("accepted an unknown plan")
	}
}

func TestSapiValidateWithoutBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SAPI_BINARY", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.luau"), []byte("return {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runSapiValidate(dir, "free", ""); err != nil {
		t.Fatal(err)
	}
	if err := runSapiValidate(dir, "free", filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing --binary was ignored")
	}
}

func TestSapiPublishSendsChannelAndActivate(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.luau"), []byte("return {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var fields map[string]string
	var gotFile bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tool/sapi/features" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"managed": false, "features": []any{}}})
			return
		}
		if r.URL.Path != "/tool/sapi/publish" {
			t.Errorf("path %s", r.URL.Path)
		}
		_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		mr := multipart.NewReader(r.Body, params["boundary"])
		fields = map[string]string{}
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			raw, _ := io.ReadAll(part)
			if part.FormName() == "file" {
				gotFile = len(raw) > 0 && raw[0] == 'P'
			} else {
				fields[part.FormName()] = string(raw)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"ok": true, "valid": true, "activated": true}})
	}))
	defer srv.Close()
	c := testClient(srv.URL)
	if err := runSapiPublish(c, dir, "Beta", true); err != nil {
		t.Fatal(err)
	}
	if fields["channel"] != "beta" || fields["activate"] != "true" || len(fields["checksum"]) != 64 || !gotFile {
		t.Fatalf("%v file=%v", fields, gotFile)
	}
}

func TestSapiExportFollowsPagesAndImportStores(t *testing.T) {
	var puts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPut {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			puts = append(puts, r.URL.Query().Get("channel")+":"+body["key"].(string))
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{}})
			return
		}
		data := map[string]any{"items": []any{map[string]any{"key": "a", "value": 1}}, "next": "a"}
		if r.URL.Query().Get("after") == "a" {
			data = map[string]any{"items": []any{map[string]any{"key": "b", "value": "two"}}, "next": nil}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
	}))
	defer srv.Close()
	c := testClient(srv.URL)
	items, err := exportSapiSlice(c, sapiSlice{scope: "channel", channel: "stable"}, "")
	if err != nil || len(items) != 2 || items[1].Key != "b" {
		t.Fatalf("%v %v", items, err)
	}
	n, err := importSapiItems(c, sapiSlice{scope: "channel", channel: "beta"}, items)
	if err != nil || n != 2 || strings.Join(puts, ",") != "beta:a,beta:b" {
		t.Fatalf("%d %v %v", n, puts, err)
	}
}

func TestSapiCommandsRegistered(t *testing.T) {
	want := []string{"publish", "validate", "test", "dev", "status", "packs", "activate", "logs", "usage", "config", "secret", "schedules", "data"}
	have := map[string]bool{}
	for _, c := range sapiCmd.Commands() {
		have[c.Name()] = true
	}
	for _, name := range want {
		if !have[name] {
			t.Fatalf("chauffeur sapi %s is missing", name)
		}
	}
}

func TestSapiResetPath(t *testing.T) {
	path, err := sapiResetPath("Channel", "beta", "", "season:", "game-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/tool/sapi/data?channel=beta&confirm=game-1&dry_run=true&prefix=season%3A&scope=channel" {
		t.Fatalf("path %q", path)
	}
	path, err = sapiResetPath("user", "", "", "", "game-1", false)
	if err != nil || path != "/tool/sapi/data?confirm=game-1&scope=user" {
		t.Fatalf("%q %v", path, err)
	}
	bad := [][5]string{
		{"", "", "", "", "g"},
		{"boards", "", "", "", "g"},
		{"global", "beta", "", "", "g"},
		{"global", "", "u1", "", "g"},
		{"channel", "", "u1", "", "g"},
		{"user", "beta", "", "", "g"},
		{"user", "", "", "save:", "g"},
		{"channel", "Bad Channel", "", "", "g"},
		{"global", "", "", "", ""},
	}
	for _, b := range bad {
		if _, err := sapiResetPath(b[0], b[1], b[2], b[3], b[4], false); err == nil {
			t.Fatalf("accepted %v", b)
		}
	}
}
