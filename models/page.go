package models

import (
	"fmt"

	"github.com/ohaiibuzzle/aidokurunner-go/postcard"
)

// PageContext mirrors Swift's `typealias PageContext = [String: String]`.
type PageContext = map[string]string

type PageContentKind uint8

const (
	PageContentKindURL PageContentKind = iota
	PageContentKindText
	PageContentKindImage
	PageContentKindZipFile
)

// PageContent mirrors AidokuRunner's PageContentCodable (the wire shape of
// Page's `content` enum). Only the fields relevant to Kind are populated.
type PageContent struct {
	Kind PageContentKind

	// Kind == URL || Kind == ZipFile
	URL string
	// Kind == URL (optional)
	Context PageContext
	// Kind == Text
	Text string
	// Kind == Image: a GlobalStore descriptor for a previously-stored image
	ImageRef int32
	// Kind == ZipFile
	FilePath string
}

func (c *PageContent) DecodePostcard(r *postcard.Reader) error {
	kind, err := r.ReadU8()
	if err != nil {
		return err
	}
	c.Kind = PageContentKind(kind)
	switch c.Kind {
	case PageContentKindURL:
		if c.URL, err = r.ReadString(); err != nil {
			return err
		}
		hasContext, err := r.ReadU8()
		if err != nil {
			return err
		}
		if hasContext == 1 {
			n, err := r.ReadLen()
			if err != nil {
				return err
			}
			c.Context = make(PageContext, n)
			for i := 0; i < n; i++ {
				k, err := r.ReadString()
				if err != nil {
					return err
				}
				v, err := r.ReadString()
				if err != nil {
					return err
				}
				c.Context[k] = v
			}
		}
	case PageContentKindText:
		if c.Text, err = r.ReadString(); err != nil {
			return err
		}
	case PageContentKindImage:
		if c.ImageRef, err = r.ReadI32(); err != nil {
			return err
		}
	case PageContentKindZipFile:
		if c.URL, err = r.ReadString(); err != nil {
			return err
		}
		if c.FilePath, err = r.ReadString(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("postcard: unknown page content kind %d", kind)
	}
	return nil
}

func (c *PageContent) EncodePostcard(w *postcard.Writer) {
	w.WriteU8(uint8(c.Kind))
	switch c.Kind {
	case PageContentKindURL:
		w.WriteString(c.URL)
		if c.Context == nil {
			w.WriteU8(0)
		} else {
			w.WriteU8(1)
			w.WriteLen(len(c.Context))
			for k, v := range c.Context {
				w.WriteString(k)
				w.WriteString(v)
			}
		}
	case PageContentKindText:
		w.WriteString(c.Text)
	case PageContentKindImage:
		w.WriteI32(c.ImageRef)
	case PageContentKindZipFile:
		w.WriteString(c.URL)
		w.WriteString(c.FilePath)
	}
}

// Page mirrors AidokuRunner's PageCodable (the wire shape of Page).
type Page struct {
	Content        PageContent
	Thumbnail      *string
	HasDescription bool
	Description    *string
}

func (p *Page) DecodePostcard(r *postcard.Reader) error {
	if err := p.Content.DecodePostcard(r); err != nil {
		return err
	}
	var err error
	if p.Thumbnail, err = decodeOptionalString(r); err != nil {
		return err
	}
	if p.HasDescription, err = r.ReadBool(); err != nil {
		return err
	}
	if p.Description, err = decodeOptionalString(r); err != nil {
		return err
	}
	return nil
}

func (p *Page) EncodePostcard(w *postcard.Writer) {
	p.Content.EncodePostcard(w)
	encodeOptionalString(w, p.Thumbnail)
	w.WriteBool(p.HasDescription)
	encodeOptionalString(w, p.Description)
}
