package cbz

import (
	"bytes"
	"encoding/xml"
)

// ComicInfo is a subset of the de facto ComicInfo.xml schema (originated by
// ComicRack, now read by Komga, Kavita, CDisplayEx, YACReader, and other
// comic/manga readers beyond this project's own KOReader plugin) that
// AidokuRunner's Manga/Chapter models can populate honestly. Fields like
// AgeRating or reading direction (ComicInfo's "Manga" element) are
// deliberately left out rather than invented: this project's own
// ContentRating/Viewer enums don't map onto ComicInfo's fixed value sets
// without editorializing values that aren't a clean 1:1.
//
// Number and Volume are kept as strings rather than ComicInfo's nominal
// xs:int for Volume: readers of this format (Komga, Kavita, ...) parse both
// leniently, and a string preserves a fractional volume/chapter number
// (e.g. "12.5") instead of silently truncating it.
type ComicInfo struct {
	XMLName         xml.Name `xml:"ComicInfo"`
	Title           string   `xml:"Title,omitempty"`
	Series          string   `xml:"Series,omitempty"`
	Number          string   `xml:"Number,omitempty"`
	Volume          string   `xml:"Volume,omitempty"`
	Summary         string   `xml:"Summary,omitempty"`
	Year            int      `xml:"Year,omitempty"`
	Month           int      `xml:"Month,omitempty"`
	Day             int      `xml:"Day,omitempty"`
	Writer          string   `xml:"Writer,omitempty"`
	Penciller       string   `xml:"Penciller,omitempty"`
	CoverArtist     string   `xml:"CoverArtist,omitempty"`
	Genre           string   `xml:"Genre,omitempty"`
	Web             string   `xml:"Web,omitempty"`
	PageCount       int      `xml:"PageCount,omitempty"`
	LanguageISO     string   `xml:"LanguageISO,omitempty"`
	ScanInformation string   `xml:"ScanInformation,omitempty"`
}

// Marshal renders c as a ComicInfo.xml document, including the XML prolog
// most readers of the format expect.
func (c ComicInfo) Marshal() ([]byte, error) {
	body, err := xml.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	buf.Write(body)
	return buf.Bytes(), nil
}
