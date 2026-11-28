package main

import (
	"strings"
	"testing"
)

func fieldValue(fields []formField, name string) (string, bool) {
	for _, f := range fields {
		if f.Name == name {
			return f.Value, true
		}
	}
	return "", false
}

func TestUploadSendsNoOfferUnlessAsked(t *testing.T) {
	u := buildFileUpload{BuildID: "b1", Platform: PlatformSpec{OS: "web", Arch: "wasm32", Channel: "stable"}, Checksum: "c"}
	for _, name := range []string{"is_demo", "for_sale", "price_cents", "designations", "embed_supported"} {
		if _, ok := fieldValue(u.fields(), name); ok {
			t.Fatalf("%s sent without being asked, which would clear what the publisher saved", name)
		}
	}
	if offerFrom(&FilesAsset{OS: "web"}) != nil {
		t.Fatal("an empty spec produced an offer")
	}
}

func TestDemoFlagIsSentWithTheFile(t *testing.T) {
	asset := &FilesAsset{OS: "web", Demo: true}
	u := buildFileUpload{BuildID: "b1", Platform: PlatformSpec{OS: "web", Arch: "wasm32", Channel: "stable"}, Checksum: "c", Offer: offerFrom(asset)}
	if v, ok := fieldValue(u.fields(), "is_demo"); !ok || v != "1" {
		t.Fatalf("is_demo = %q, %v", v, ok)
	}
	if v, _ := fieldValue(u.fields(), "for_sale"); v != "0" {
		t.Fatalf("for_sale = %q", v)
	}
}

func TestValidateOfferRefusesWhatTheServiceRefuses(t *testing.T) {
	cases := []struct {
		name  string
		asset FilesAsset
		want  string
	}{
		{"demo on a download", FilesAsset{OS: "windows", Demo: true}, "needs os web"},
		{"priced demo", FilesAsset{OS: "web", Demo: true, PriceCents: 100}, "cannot be priced"},
		{"sale without price", FilesAsset{OS: "web", ForSale: true}, "needs --price-cents"},
		{"priced embed", FilesAsset{OS: "web", EmbedSupported: true, ForSale: true, PriceCents: 5}, "cannot be priced"},
		{"negative price", FilesAsset{OS: "web", PriceCents: -1}, "zero or more"},
	}
	for _, c := range cases {
		a := c.asset
		err := validateOffer(&a)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
	ok := FilesAsset{OS: "web", Demo: true}
	if err := validateOffer(&ok); err != nil {
		t.Fatalf("plain web demo refused: %v", err)
	}
}
