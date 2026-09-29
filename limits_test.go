package main

import (
	"errors"
	"testing"
)

func TestValidatePlatform(t *testing.T) {
	p, err := validatePlatform(" Darwin ", "AARCH64", "")
	if err != nil || p != (PlatformSpec{OS: "macos", Arch: "arm64", Channel: "stable"}) {
		t.Fatalf("p=%+v err=%v", p, err)
	}
	for _, bad := range [][3]string{{"beos", "x86_64", ""}, {"linux", "mips", ""}, {"linux", "x86_64", "Beta Channel"}, {"linux", "x86_64", "-beta"}} {
		if _, err := validatePlatform(bad[0], bad[1], bad[2]); exitCode(err) != exitUsage {
			t.Fatalf("%v accepted: %v", bad, err)
		}
	}
}

func TestValidateFilterAllowsEmpty(t *testing.T) {
	if o, a, c, err := validateFilter("", "", ""); err != nil || o+a+c != "" {
		t.Fatal(err)
	}
	if _, _, _, err := validateFilter("amiga", "", ""); err == nil {
		t.Fatal("bad os accepted")
	}
}

func TestValidateEngineVersion(t *testing.T) {
	for in, want := range map[string]string{"4.3": "4.3", "v4.3.1": "4.3.1", " 4.3.0-Beta.2 ": "4.3.0-beta.2", "": ""} {
		got, err := validateEngineVersion(in)
		if err != nil || got != want {
			t.Fatalf("%q -> %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"four", "4.3.1.2.5", "4.3; rm -rf", "4.3-"} {
		if _, err := validateEngineVersion(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestValidateBuildFields(t *testing.T) {
	ok := &BuildAsset{Title: "T", Version: "1.0", Description: "D", Video: "https://youtu.be/x"}
	if err := validateBuildFields(ok); err != nil {
		t.Fatal(err)
	}
	long := make([]byte, 33)
	for i := range long {
		long[i] = '1'
	}
	for _, bad := range []*BuildAsset{
		{Title: "T", Version: string(long)},
		{Title: "T", Version: "1", Video: "javascript:alert(1)"},
		{Title: "T", Version: "1", Video: "ftp://x/y"},
		{Title: "T", Version: "1", Changelog: make([]ChangelogEntry, maxChangelogItems+1)},
	} {
		if err := validateBuildFields(bad); err == nil {
			t.Fatalf("%+v accepted", bad)
		}
	}
}

func TestCheckServiceURL(t *testing.T) {
	for _, ok := range []string{"https://api.blazium.online/api/v1", "http://localhost:8080/api/v1", "http://127.0.0.1:9000"} {
		if err := checkServiceURL(ok); err != nil {
			t.Fatalf("%s rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://api.blazium.online/api/v1", "https://user:pass@api.blazium.online", "file:///etc/passwd", "not a url"} {
		if err := checkServiceURL(bad); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
}

func TestExitCodes(t *testing.T) {
	cases := map[int]error{
		exitOK:      nil,
		exitUsage:   usageErrorf("bad"),
		exitAPI:     &apiError{Status: 400, Code: 4026},
		exitNetwork: &networkError{err: errors.New("dial tcp: refused")},
	}
	for want, err := range cases {
		if got := exitCode(err); got != want {
			t.Fatalf("exitCode(%v) = %d, want %d", err, got, want)
		}
	}
	wrapped := errors.Join(errors.New("context"), &apiError{Status: 500})
	if exitCode(wrapped) != exitAPI {
		t.Fatal("wrapped API errors must keep exit code 2")
	}
}
