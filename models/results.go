package models

import "github.com/ohaiibuzzle/aidokurunner-go/postcard"

// MangaPageResult mirrors AidokuRunner's MangaPageResult.swift. Decoded
// guest->host from get_search_manga_list/get_manga_list.
type MangaPageResult struct {
	Entries     []Manga
	HasNextPage bool
}

func (m *MangaPageResult) DecodePostcard(r *postcard.Reader) error {
	n, err := r.ReadLen()
	if err != nil {
		return err
	}
	m.Entries = make([]Manga, n)
	for i := 0; i < n; i++ {
		if err := m.Entries[i].DecodePostcard(r); err != nil {
			return err
		}
	}
	if m.HasNextPage, err = r.ReadBool(); err != nil {
		return err
	}
	return nil
}

// SetSourceKey stamps SourceKey on every entry, matching
// MangaPageResult.setSourceKey in Source.swift.
func (m *MangaPageResult) SetSourceKey(key string) {
	for i := range m.Entries {
		m.Entries[i].SourceKey = key
	}
}

// MangaWithChapter mirrors AidokuRunner's MangaWithChapter.swift. Decoded
// guest->host as part of Home's mangaChapterList component.
type MangaWithChapter struct {
	Manga   Manga
	Chapter Chapter
}

func (mc *MangaWithChapter) DecodePostcard(r *postcard.Reader) error {
	if err := mc.Manga.DecodePostcard(r); err != nil {
		return err
	}
	return mc.Chapter.DecodePostcard(r)
}

// DeepLinkResult mirrors AidokuRunner's DeepLink.swift. Decoded guest->host
// from handle_deep_link as Option<DeepLinkResult>.
type DeepLinkResult struct {
	MangaKey   *string
	ChapterKey *string
	Listing    *Listing
}

func (d *DeepLinkResult) DecodePostcard(r *postcard.Reader) error {
	var err error
	if d.MangaKey, err = decodeOptionalString(r); err != nil {
		return err
	}
	if d.ChapterKey, err = decodeOptionalString(r); err != nil {
		return err
	}
	if d.Listing, err = decodeOptionalListing(r); err != nil {
		return err
	}
	return nil
}

// DecodeOptionalDeepLinkResult decodes an Option<DeepLinkResult>, matching
// handle_deep_link's `DeepLinkResult?` return type.
func DecodeOptionalDeepLinkResult(r *postcard.Reader) (*DeepLinkResult, error) {
	present, err := r.ReadOptionTag()
	if err != nil || !present {
		return nil, err
	}
	d := &DeepLinkResult{}
	if err := d.DecodePostcard(r); err != nil {
		return nil, err
	}
	return d, nil
}

// ImageRef is a GlobalStore descriptor pointing at a previously-stored
// image, matching Swift's `typealias ImageRef = Int32`.
type ImageRef = int32

// Request mirrors AidokuRunner's Request (Response.swift). Encoded
// host->guest, nested in Response.
type Request struct {
	URL     *string
	Headers map[string]string
}

func (r *Request) EncodePostcard(w *postcard.Writer) {
	encodeOptionalString(w, r.URL)
	encodeStringMap(w, r.Headers)
}

// Response mirrors AidokuRunner's Response (Response.swift). Encoded
// host->guest for process_page_image.
type Response struct {
	Code    uint16
	Headers map[string]string
	Request Request
	Image   ImageRef
}

func (resp *Response) EncodePostcard(w *postcard.Writer) {
	w.WriteU16(resp.Code)
	encodeStringMap(w, resp.Headers)
	resp.Request.EncodePostcard(w)
	w.WriteI32(resp.Image)
}

// SourceFeatures mirrors AidokuRunner's SourceFeatures.swift: which
// optional guest exports were detected at load time. Populated by the
// runtime package, not decoded from the wire.
type SourceFeatures struct {
	ProvidesListings         bool
	ProvidesHome             bool
	DynamicFilters           bool
	DynamicSettings          bool
	DynamicListings          bool
	ProcessesPages           bool
	ProcessesCovers          bool
	ProvidesImageRequests    bool
	ProvidesPageDescriptions bool
	ProvidesAlternateCovers  bool
	ProvidesBaseURL          bool
	HandlesNotifications     bool
	HandlesDeepLinks         bool
	HandlesBasicLogin        bool
	HandlesWebLogin          bool
	HandlesMigration         bool
}

// KeyKind mirrors AidokuRunner's KeyKind.swift. Passed as a raw i32 call
// argument to handle_key_migration, never postcard-encoded.
type KeyKind int32

const (
	KeyKindManga   KeyKind = 0
	KeyKindChapter KeyKind = 1
)
