package models

import (
	"encoding/json"
	"testing"
)

func TestSourceInfoJSON(t *testing.T) {
	// Mirrors the bundled AidokuRunner test fixture's source.json.
	data := []byte(`{
		"info": {
			"id": "test",
			"name": "Test",
			"version": 1,
			"url": "https://aidoku.app",
			"contentRating": 0,
			"languages": ["en"]
		}
	}`)
	var info SourceInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if info.Info.ID != "test" || info.Info.Name != "Test" || info.Info.Version != 1 {
		t.Fatalf("unexpected info: %+v", info.Info)
	}
	if info.Info.ContentRating == nil || *info.Info.ContentRating != SourceContentRatingSafe {
		t.Fatalf("unexpected content rating: %+v", info.Info.ContentRating)
	}
	if len(info.Info.Languages) != 1 || info.Info.Languages[0] != "en" {
		t.Fatalf("unexpected languages: %+v", info.Info.Languages)
	}
}

func TestSourceInfoListingBareString(t *testing.T) {
	var l SourceInfoListing
	if err := json.Unmarshal([]byte(`"Popular"`), &l); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if l.Listing.ID != "Popular" || l.Listing.Name != "Popular" {
		t.Fatalf("unexpected listing: %+v", l.Listing)
	}
}

func TestSourceInfoListingObject(t *testing.T) {
	var l SourceInfoListing
	if err := json.Unmarshal([]byte(`{"id": "1", "name": "Latest", "kind": 1}`), &l); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if l.Listing.ID != "1" || l.Listing.Name != "Latest" || l.Listing.Kind != ListingKindList {
		t.Fatalf("unexpected listing: %+v", l.Listing)
	}
}
