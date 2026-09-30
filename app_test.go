package main

import (
	"testing"
)

func TestParseAppSpec(t *testing.T) {
	p := writeYAML(t, `version: v1
spec: build
asset:
  title: My Game
  type: game
  description: A game
  version: 1.0.0
  app:
    id: " Server "
    name: Dedicated Server
`)
	cfg, err := ParseYAML(p)
	if err != nil {
		t.Fatal(err)
	}
	if a := cfg.BuildAsset.App; a == nil || a.ID != "server" || a.Name != "Dedicated Server" {
		t.Fatalf("app = %+v", a)
	}

	files := writeYAML(t, `version: v1
spec: addfiles
asset:
  type: game
  version: 1.0.0
  os: linux
  arch: x86_64
  channel: stable
  app:
    id: server
  files:
    - file: a.bin
`)
	fcfg, err := ParseYAML(files)
	if err != nil {
		t.Fatal(err)
	}
	if fcfg.FilesAsset.App.id() != "server" {
		t.Fatalf("files app = %+v", fcfg.FilesAsset.App)
	}
}

func TestValidateAppRejectsBadValues(t *testing.T) {
	long := ""
	for i := 0; i < 81; i++ {
		long += "x"
	}
	for name, a := range map[string]*AppSpec{
		"chars":       {ID: "my_app"},
		"leadingdash": {ID: "-server"},
		"toolong":     {ID: "abcdefghijklmnopqrstuvwxyz0123456789"},
		"nameonly":    {Name: "Server"},
		"longname":    {ID: "server", Name: long},
	} {
		if err := validateApp(a); exitCode(err) != exitUsage {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	for _, a := range []*AppSpec{nil, {}, {ID: "server"}, {ID: "Level-Editor", Name: "Level Editor"}} {
		if err := validateApp(a); err != nil {
			t.Errorf("%+v: %v", a, err)
		}
	}
}

func TestAppAddToAndFlags(t *testing.T) {
	req := map[string]any{}
	(*AppSpec)(nil).addTo(req)
	(&AppSpec{}).addTo(req)
	if len(req) != 0 {
		t.Fatalf("main app should add nothing: %v", req)
	}
	(&AppSpec{ID: "server", Name: "Server"}).addTo(req)
	if req["app"] != "server" || req["app_name"] != "Server" {
		t.Fatalf("req = %v", req)
	}

	file := &AppSpec{ID: "server", Name: "Server"}
	if got := appFromFlags(file, "", ""); got != file {
		t.Fatalf("no flags should keep the file's app")
	}
	got := appFromFlags(file, "editor", "")
	if got.ID != "editor" || got.Name != "Server" || file.ID != "server" {
		t.Fatalf("got %+v, file %+v", got, file)
	}
	if got := appFromFlags(nil, "server", "Dedicated"); got.ID != "server" || got.Name != "Dedicated" {
		t.Fatalf("got %+v", got)
	}
}

func TestBuildFileUploadFieldsIncludeApp(t *testing.T) {
	has := func(fs []formField, name string) string {
		for _, f := range fs {
			if f.Name == name {
				return f.Value
			}
		}
		return ""
	}
	u := buildFileUpload{BuildID: "b1", Platform: testPlatform, Checksum: "c"}
	if has(u.fields(), "app") != "" {
		t.Fatal("main app upload should not send app")
	}
	u.App = "server"
	if has(u.fields(), "app") != "server" {
		t.Fatalf("fields = %+v", u.fields())
	}
}
