package models

import (
	"testing"

	"github.com/ohaiibuzzle/aidokurunner-go/postcard"
)

// TestPageContentDecodePostcardRejectsUnknownKind and
// TestFilterValueDecodePostcardRejectsUnknownKind guard against a silent
// reader desync: without a default case, an unrecognized kind byte (a
// forward-compat mismatch, or corrupt data) decoded as a no-op instead of
// an error, leaving every field after it misaligned with whatever the
// encoder actually wrote.
func TestPageContentDecodePostcardRejectsUnknownKind(t *testing.T) {
	w := postcard.NewWriter()
	w.WriteU8(99) // not a valid PageContentKind
	r := postcard.NewReader(w.Bytes())

	var c PageContent
	if err := c.DecodePostcard(r); err == nil {
		t.Fatal("expected an error for an unknown page content kind, got nil")
	}
}

func TestFilterValueDecodePostcardRejectsUnknownKind(t *testing.T) {
	w := postcard.NewWriter()
	w.WriteU8(99) // not a valid FilterValueKind
	w.WriteString("some-id")
	r := postcard.NewReader(w.Bytes())

	var f FilterValue
	if err := f.DecodePostcard(r); err == nil {
		t.Fatal("expected an error for an unknown filter value kind, got nil")
	}
}
