package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMintKeyRejectsBadIDBeforeRequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := testClient(srv.URL)
	for _, id := range []string{"", " ", "not-a-uid", "../tool/upload", "5f1c2d3e-0000-4000-8000-000000000000?x=1"} {
		if _, err := renameMintKey(c, id, "name"); err == nil || exitCode(err) != exitUsage {
			t.Fatalf("rename %q: %v", id, err)
		}
		if _, err := revokeMintKey(c, id); err == nil || exitCode(err) != exitUsage {
			t.Fatalf("revoke %q: %v", id, err)
		}
	}
	if called {
		t.Fatal("request sent for an invalid mint key id")
	}
	const id = "5f1c2d3e-0000-4000-8000-000000000000"
	path, err := mintKeyPath(id)
	if err != nil || path != "/tool/mint-keys/"+id {
		t.Fatalf("path = %q, %v", path, err)
	}
}

func TestMintKeyUpdateTargetsTheUID(t *testing.T) {
	const id = "5f1c2d3e-0000-4000-8000-000000000000"
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"id": id}})
	}))
	defer srv.Close()
	c := testClient(srv.URL)
	if _, err := renameMintKey(c, id, "name"); err != nil {
		t.Fatal(err)
	}
	if _, err := revokeMintKey(c, id); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "PUT /tool/mint-keys/"+id || got[1] != "DELETE /tool/mint-keys/"+id {
		t.Fatalf("requests = %v", got)
	}
}
