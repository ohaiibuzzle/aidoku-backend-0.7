package models

import "encoding/json"

type SourceContentRating int

const (
	SourceContentRatingSafe SourceContentRating = iota
	SourceContentRatingContainsNsfw
	SourceContentRatingPrimarilyNsfw
)

type LanguageSelectType string

const (
	LanguageSelectTypeSingle   LanguageSelectType = "single"
	LanguageSelectTypeMultiple LanguageSelectType = "multiple"
)

// SourceInfo mirrors AidokuRunner's SourceInfo.swift: the shape of
// source.json.
type SourceInfo struct {
	Info     SourceInfoDetails    `json:"info"`
	Listings []SourceInfoListing  `json:"listings"`
	Config   *SourceConfiguration `json:"config"`
}

type SourceInfoDetails struct {
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	AltNames      []string             `json:"altNames"`
	Version       int                  `json:"version"`
	URL           *string              `json:"url"`
	URLs          []string             `json:"urls"`
	ContentRating *SourceContentRating `json:"contentRating"`
	Languages     []string             `json:"languages"`
}

// SourceInfoListing mirrors SourceInfo.InfoListing, which accepts either a
// bare string (used as both id and name) or a full Listing object.
type SourceInfoListing struct {
	Listing Listing
}

func (l *SourceInfoListing) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err == nil {
		l.Listing = Listing{ID: name, Name: name}
		return nil
	}
	return json.Unmarshal(data, &l.Listing)
}

type SourceConfiguration struct {
	LanguageSelectType         *LanguageSelectType `json:"languageSelectType"`
	SupportsArtistSearch       *bool               `json:"supportsArtistSearch"`
	SupportsAuthorSearch       *bool               `json:"supportsAuthorSearch"`
	SupportsTagSearch          *bool               `json:"supportsTagSearch"`
	AllowsBaseURLSelect        *bool               `json:"allowsBaseUrlSelect"`
	BreakingChangeVersion      *int                `json:"breakingChangeVersion"`
	HidesFiltersWhileSearching *bool               `json:"hidesFiltersWhileSearching"`
	MaximumParallelRequests    *int                `json:"maximumParallelRequests"`
}
