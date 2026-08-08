package models

import (
	"time"

	"github.com/ohaiibuzzle/aidokurunner-go/postcard"
)

// Chapter mirrors AidokuRunner's Chapter.swift. Field order is
// load-bearing for the postcard wire format.
type Chapter struct {
	Key           string
	Title         *string
	ChapterNumber *float32
	VolumeNumber  *float32
	DateUploaded  *time.Time
	Scanlators    []string
	URL           *string
	Language      *string
	Thumbnail     *string
	Locked        bool
}

func (c *Chapter) DecodePostcard(r *postcard.Reader) error {
	var err error
	if c.Key, err = r.ReadString(); err != nil {
		return err
	}
	if c.Title, err = decodeOptionalString(r); err != nil {
		return err
	}
	if c.ChapterNumber, err = decodeOptionalF32(r); err != nil {
		return err
	}
	if c.VolumeNumber, err = decodeOptionalF32(r); err != nil {
		return err
	}
	if c.DateUploaded, err = decodeOptionalEpoch(r); err != nil {
		return err
	}
	if c.Scanlators, err = decodeOptionalStringSlice(r); err != nil {
		return err
	}
	if c.URL, err = decodeOptionalString(r); err != nil {
		return err
	}
	if c.Language, err = decodeOptionalString(r); err != nil {
		return err
	}
	if c.Thumbnail, err = decodeOptionalString(r); err != nil {
		return err
	}
	if c.Locked, err = r.ReadBool(); err != nil {
		return err
	}
	return nil
}

func (c *Chapter) EncodePostcard(w *postcard.Writer) {
	w.WriteString(c.Key)
	encodeOptionalString(w, c.Title)
	encodeOptionalF32(w, c.ChapterNumber)
	encodeOptionalF32(w, c.VolumeNumber)
	encodeOptionalEpoch(w, c.DateUploaded)
	encodeOptionalStringSlice(w, c.Scanlators)
	encodeOptionalString(w, c.URL)
	encodeOptionalString(w, c.Language)
	encodeOptionalString(w, c.Thumbnail)
	w.WriteBool(c.Locked)
}
