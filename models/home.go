package models

import (
	"fmt"

	"github.com/ohaiibuzzle/aidokurunner-go/postcard"
)

// Home mirrors AidokuRunner's Home.swift. Decoded guest->host from
// get_home (and streamed incrementally via send_partial_result).
type Home struct {
	Components []HomeComponent
}

func (h *Home) DecodePostcard(r *postcard.Reader) error {
	n, err := r.ReadLen()
	if err != nil {
		return err
	}
	h.Components = make([]HomeComponent, n)
	for i := 0; i < n; i++ {
		if err := h.Components[i].DecodePostcard(r); err != nil {
			return err
		}
	}
	return nil
}

// SetSourceKey stamps SourceKey on every Manga reachable from this Home,
// matching Home.setSourceKey in Home.swift.
func (h *Home) SetSourceKey(key string) {
	for i := range h.Components {
		h.Components[i].SetSourceKey(key)
	}
}

type HomeComponentKind uint8

const (
	HomeComponentKindImageScroller HomeComponentKind = iota
	HomeComponentKindBigScroller
	HomeComponentKindScroller
	HomeComponentKindMangaList
	HomeComponentKindMangaChapterList
	HomeComponentKindFilters
	HomeComponentKindLinks
)

// HomeComponent mirrors AidokuRunner's HomeComponent (Home.swift). Only the
// fields relevant to Kind are populated.
type HomeComponent struct {
	Title    *string
	Subtitle *string
	Kind     HomeComponentKind

	// Kind == ImageScroller
	ImageScrollerLinks  []HomeLink
	AutoScrollInterval  *float32 // shared by ImageScroller/BigScroller
	ImageScrollerWidth  *int64
	ImageScrollerHeight *int64

	// Kind == BigScroller
	BigScrollerEntries []Manga

	// Kind == Scroller
	ScrollerEntries []HomeLink
	ScrollerListing *Listing

	// Kind == MangaList
	MangaListRanking  bool
	MangaListPageSize *int64
	MangaListEntries  []HomeLink
	MangaListListing  *Listing

	// Kind == MangaChapterList
	MangaChapterListPageSize *int64
	MangaChapterListEntries  []MangaWithChapter
	MangaChapterListListing  *Listing

	// Kind == Filters
	FilterItems []HomeFilterItem

	// Kind == Links
	Links []HomeLink
}

func (c *HomeComponent) DecodePostcard(r *postcard.Reader) error {
	var err error
	if c.Title, err = decodeOptionalString(r); err != nil {
		return err
	}
	if c.Subtitle, err = decodeOptionalString(r); err != nil {
		return err
	}
	kind, err := r.ReadU8()
	if err != nil {
		return err
	}
	c.Kind = HomeComponentKind(kind)

	switch c.Kind {
	case HomeComponentKindImageScroller:
		if c.ImageScrollerLinks, err = decodeHomeLinkSlice(r); err != nil {
			return err
		}
		if c.AutoScrollInterval, err = decodeOptionalF32(r); err != nil {
			return err
		}
		if c.ImageScrollerWidth, err = decodeOptionalI64(r); err != nil {
			return err
		}
		if c.ImageScrollerHeight, err = decodeOptionalI64(r); err != nil {
			return err
		}
	case HomeComponentKindBigScroller:
		n, err := r.ReadLen()
		if err != nil {
			return err
		}
		c.BigScrollerEntries = make([]Manga, n)
		for i := 0; i < n; i++ {
			if err := c.BigScrollerEntries[i].DecodePostcard(r); err != nil {
				return err
			}
		}
		if c.AutoScrollInterval, err = decodeOptionalF32(r); err != nil {
			return err
		}
	case HomeComponentKindScroller:
		if c.ScrollerEntries, err = decodeHomeLinkSlice(r); err != nil {
			return err
		}
		if c.ScrollerListing, err = decodeOptionalListing(r); err != nil {
			return err
		}
	case HomeComponentKindMangaList:
		if c.MangaListRanking, err = r.ReadBool(); err != nil {
			return err
		}
		if c.MangaListPageSize, err = decodeOptionalI64(r); err != nil {
			return err
		}
		if c.MangaListEntries, err = decodeHomeLinkSlice(r); err != nil {
			return err
		}
		if c.MangaListListing, err = decodeOptionalListing(r); err != nil {
			return err
		}
	case HomeComponentKindMangaChapterList:
		if c.MangaChapterListPageSize, err = decodeOptionalI64(r); err != nil {
			return err
		}
		n, err := r.ReadLen()
		if err != nil {
			return err
		}
		c.MangaChapterListEntries = make([]MangaWithChapter, n)
		for i := 0; i < n; i++ {
			if err := c.MangaChapterListEntries[i].DecodePostcard(r); err != nil {
				return err
			}
		}
		if c.MangaChapterListListing, err = decodeOptionalListing(r); err != nil {
			return err
		}
	case HomeComponentKindFilters:
		n, err := r.ReadLen()
		if err != nil {
			return err
		}
		c.FilterItems = make([]HomeFilterItem, n)
		for i := 0; i < n; i++ {
			if err := c.FilterItems[i].decodePostcard(r); err != nil {
				return err
			}
		}
	case HomeComponentKindLinks:
		if c.Links, err = decodeHomeLinkSlice(r); err != nil {
			return err
		}
	default:
		return fmt.Errorf("postcard: unknown home component kind %d", kind)
	}
	return nil
}

// SetSourceKey mirrors HomeComponent.setSourceKey in Home.swift.
func (c *HomeComponent) SetSourceKey(key string) {
	switch c.Kind {
	case HomeComponentKindImageScroller:
		for i := range c.ImageScrollerLinks {
			c.ImageScrollerLinks[i].SetSourceKey(key)
		}
	case HomeComponentKindBigScroller:
		for i := range c.BigScrollerEntries {
			c.BigScrollerEntries[i].SourceKey = key
		}
	case HomeComponentKindScroller:
		for i := range c.ScrollerEntries {
			c.ScrollerEntries[i].SetSourceKey(key)
		}
	case HomeComponentKindMangaList:
		for i := range c.MangaListEntries {
			c.MangaListEntries[i].SetSourceKey(key)
		}
	case HomeComponentKindMangaChapterList:
		for i := range c.MangaChapterListEntries {
			c.MangaChapterListEntries[i].Manga.SourceKey = key
		}
	case HomeComponentKindLinks:
		for i := range c.Links {
			c.Links[i].SetSourceKey(key)
		}
	}
}

type HomeLinkValueKind uint8

const (
	HomeLinkValueKindURL HomeLinkValueKind = iota
	HomeLinkValueKindListing
	HomeLinkValueKindManga
)

// HomeLinkValue mirrors HomeComponent.Value.LinkValue.
type HomeLinkValue struct {
	Kind    HomeLinkValueKind
	URL     string
	Listing Listing
	Manga   Manga
}

func (v *HomeLinkValue) decodePostcard(r *postcard.Reader) error {
	kind, err := r.ReadU8()
	if err != nil {
		return err
	}
	v.Kind = HomeLinkValueKind(kind)
	switch v.Kind {
	case HomeLinkValueKindURL:
		if v.URL, err = r.ReadString(); err != nil {
			return err
		}
	case HomeLinkValueKindListing:
		if err := v.Listing.DecodePostcard(r); err != nil {
			return err
		}
	case HomeLinkValueKindManga:
		if err := v.Manga.DecodePostcard(r); err != nil {
			return err
		}
	default:
		return fmt.Errorf("postcard: unknown home link value kind %d", kind)
	}
	return nil
}

// HomeLink mirrors HomeComponent.Value.Link.
type HomeLink struct {
	Title    string
	Subtitle *string
	ImageURL *string
	Value    *HomeLinkValue
}

func (l *HomeLink) decodePostcard(r *postcard.Reader) error {
	var err error
	if l.Title, err = r.ReadString(); err != nil {
		return err
	}
	if l.Subtitle, err = decodeOptionalString(r); err != nil {
		return err
	}
	if l.ImageURL, err = decodeOptionalString(r); err != nil {
		return err
	}
	present, err := r.ReadOptionTag()
	if err != nil {
		return err
	}
	if present {
		v := &HomeLinkValue{}
		if err := v.decodePostcard(r); err != nil {
			return err
		}
		l.Value = v
	}
	return nil
}

// SetSourceKey mirrors Link.setSourceKey in Home.swift.
func (l *HomeLink) SetSourceKey(key string) {
	if l.Value != nil && l.Value.Kind == HomeLinkValueKindManga {
		l.Value.Manga.SourceKey = key
	}
}

func decodeHomeLinkSlice(r *postcard.Reader) ([]HomeLink, error) {
	n, err := r.ReadLen()
	if err != nil {
		return nil, err
	}
	out := make([]HomeLink, n)
	for i := 0; i < n; i++ {
		if err := out[i].decodePostcard(r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// HomeFilterItem mirrors HomeComponent.Value.FilterItem.
type HomeFilterItem struct {
	Title  string
	Values []FilterValue
}

func (f *HomeFilterItem) decodePostcard(r *postcard.Reader) error {
	var err error
	if f.Title, err = r.ReadString(); err != nil {
		return err
	}
	present, err := r.ReadOptionTag()
	if err != nil {
		return err
	}
	if present {
		if f.Values, err = DecodeFilterValueSlice(r); err != nil {
			return err
		}
	}
	return nil
}

type HomePartialResultKind uint8

const (
	HomePartialResultKindLayout HomePartialResultKind = iota
	HomePartialResultKindComponent
)

// HomePartialResult mirrors HomePartialResult (Home.swift), used to decode
// send_partial_result payloads while get_home is streaming.
type HomePartialResult struct {
	Kind      HomePartialResultKind
	Layout    Home
	Component HomeComponent
}

func (p *HomePartialResult) DecodePostcard(r *postcard.Reader) error {
	kind, err := r.ReadU8()
	if err != nil {
		return err
	}
	p.Kind = HomePartialResultKind(kind)
	switch p.Kind {
	case HomePartialResultKindLayout:
		return p.Layout.DecodePostcard(r)
	case HomePartialResultKindComponent:
		return p.Component.DecodePostcard(r)
	default:
		return fmt.Errorf("postcard: unknown home partial result kind %d", kind)
	}
}
