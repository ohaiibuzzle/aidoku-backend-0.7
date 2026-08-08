package models

import (
	"github.com/ohaiibuzzle/aidokurunner-go/postcard"
)

type PublishingStatus uint8

const (
	PublishingStatusUnknown PublishingStatus = iota
	PublishingStatusOngoing
	PublishingStatusCompleted
	PublishingStatusCancelled
	PublishingStatusHiatus
)

type ContentRating uint8

const (
	ContentRatingUnknown ContentRating = iota
	ContentRatingSafe
	ContentRatingSuggestive
	ContentRatingNsfw
)

type Viewer uint8

const (
	ViewerUnknown Viewer = iota
	ViewerLeftToRight
	ViewerRightToLeft
	ViewerVertical
	ViewerWebtoon
)

type UpdateStrategy uint8

const (
	UpdateStrategyAlways UpdateStrategy = iota
	UpdateStrategyNever
)

// Manga mirrors AidokuRunner's Manga.swift. Field order matches the Swift
// struct's declaration order, which is load-bearing for the postcard wire
// format (structs are encoded as sequential untagged fields).
type Manga struct {
	// SourceKey is excluded from encoding (matches @ExcludedFromCoding) and
	// is populated by the host after decoding a guest response.
	SourceKey string

	Key            string
	Title          string
	Cover          *string
	Artists        []string
	Authors        []string
	Description    *string
	URL            *string
	Tags           []string
	Status         PublishingStatus
	ContentRating  ContentRating
	Viewer         Viewer
	UpdateStrategy UpdateStrategy
	NextUpdateTime *int64
	Chapters       []Chapter
}

func (m *Manga) DecodePostcard(r *postcard.Reader) error {
	var err error
	if m.Key, err = r.ReadString(); err != nil {
		return err
	}
	if m.Title, err = r.ReadString(); err != nil {
		return err
	}
	if m.Cover, err = decodeOptionalString(r); err != nil {
		return err
	}
	if m.Artists, err = decodeOptionalStringSlice(r); err != nil {
		return err
	}
	if m.Authors, err = decodeOptionalStringSlice(r); err != nil {
		return err
	}
	if m.Description, err = decodeOptionalString(r); err != nil {
		return err
	}
	if m.URL, err = decodeOptionalString(r); err != nil {
		return err
	}
	if m.Tags, err = decodeOptionalStringSlice(r); err != nil {
		return err
	}
	statusByte, err := r.ReadU8()
	if err != nil {
		return err
	}
	m.Status = PublishingStatus(statusByte)
	contentRatingByte, err := r.ReadU8()
	if err != nil {
		return err
	}
	m.ContentRating = ContentRating(contentRatingByte)
	viewerByte, err := r.ReadU8()
	if err != nil {
		return err
	}
	m.Viewer = Viewer(viewerByte)
	updateStrategyByte, err := r.ReadU8()
	if err != nil {
		return err
	}
	m.UpdateStrategy = UpdateStrategy(updateStrategyByte)
	if m.NextUpdateTime, err = decodeOptionalI64(r); err != nil {
		return err
	}
	present, err := r.ReadOptionTag()
	if err != nil {
		return err
	}
	if present {
		n, err := r.ReadLen()
		if err != nil {
			return err
		}
		m.Chapters = make([]Chapter, n)
		for i := 0; i < n; i++ {
			if err := m.Chapters[i].DecodePostcard(r); err != nil {
				return err
			}
		}
	} else {
		m.Chapters = nil
	}
	return nil
}

func (m *Manga) EncodePostcard(w *postcard.Writer) {
	w.WriteString(m.Key)
	w.WriteString(m.Title)
	encodeOptionalString(w, m.Cover)
	encodeOptionalStringSlice(w, m.Artists)
	encodeOptionalStringSlice(w, m.Authors)
	encodeOptionalString(w, m.Description)
	encodeOptionalString(w, m.URL)
	encodeOptionalStringSlice(w, m.Tags)
	w.WriteU8(uint8(m.Status))
	w.WriteU8(uint8(m.ContentRating))
	w.WriteU8(uint8(m.Viewer))
	w.WriteU8(uint8(m.UpdateStrategy))
	encodeOptionalI64(w, m.NextUpdateTime)
	if m.Chapters == nil {
		w.WriteNone()
	} else {
		w.WriteSome()
		w.WriteLen(len(m.Chapters))
		for i := range m.Chapters {
			m.Chapters[i].EncodePostcard(w)
		}
	}
}
