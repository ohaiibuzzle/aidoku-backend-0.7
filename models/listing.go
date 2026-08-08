package models

import (
	"encoding/json"

	"github.com/ohaiibuzzle/aidokurunner-go/postcard"
)

type ListingKind uint8

const (
	ListingKindDefault ListingKind = iota
	ListingKindList
)

// Listing mirrors AidokuRunner's Listing.swift. Used both in JSON manifests
// (source.json's `listings`) and over postcard (dynamic get_listings,
// nested in Home components).
type Listing struct {
	ID   string      `json:"id"`
	Name string      `json:"name"`
	Kind ListingKind `json:"kind"`
}

func (l *Listing) UnmarshalJSON(data []byte) error {
	var raw struct {
		ID   string  `json:"id"`
		Name *string `json:"name"`
		Kind *uint8  `json:"kind"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	l.ID = raw.ID
	if raw.Name != nil {
		l.Name = *raw.Name
	} else {
		l.Name = raw.ID
	}
	if raw.Kind != nil {
		l.Kind = ListingKind(*raw.Kind)
	} else {
		l.Kind = ListingKindDefault
	}
	return nil
}

func (l *Listing) DecodePostcard(r *postcard.Reader) error {
	var err error
	if l.ID, err = r.ReadString(); err != nil {
		return err
	}
	if l.Name, err = r.ReadString(); err != nil {
		return err
	}
	kind, err := r.ReadU8()
	if err != nil {
		return err
	}
	l.Kind = ListingKind(kind)
	return nil
}

func (l *Listing) EncodePostcard(w *postcard.Writer) {
	w.WriteString(l.ID)
	w.WriteString(l.Name)
	w.WriteU8(uint8(l.Kind))
}

func decodeOptionalListing(r *postcard.Reader) (*Listing, error) {
	present, err := r.ReadOptionTag()
	if err != nil || !present {
		return nil, err
	}
	l := &Listing{}
	if err := l.DecodePostcard(r); err != nil {
		return nil, err
	}
	return l, nil
}

func encodeOptionalListing(w *postcard.Writer, l *Listing) {
	if l == nil {
		w.WriteNone()
		return
	}
	w.WriteSome()
	l.EncodePostcard(w)
}
