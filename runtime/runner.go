package runtime

import (
	"context"

	"github.com/tetratelabs/wazero/api"

	"github.com/ohaiibuzzle/aidokurunner-go/models"
	"github.com/ohaiibuzzle/aidokurunner-go/postcard"
	"github.com/ohaiibuzzle/aidokurunner-go/runtime/host"
)

// This file mirrors the `extension Interpreter: Runner` methods in
// Interpreter.swift. Every "pointer" passed as a guest call argument here is
// a host Store descriptor (see host.Store) that the guest pulls via
// std.buffer_len/std.read_buffer — not a guest memory address. The host
// never calls the guest's `alloc` export; that's purely a test-harness
// convenience for exercising individual namespace functions in isolation.

func boolToI32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

func writeStringSlice(w *postcard.Writer, s []string) {
	w.WriteLen(len(s))
	for _, v := range s {
		w.WriteString(v)
	}
}

func (i *Interpreter) GetSearchMangaList(ctx context.Context, query *string, page int, filters []models.FilterValue) (*models.MangaPageResult, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	q := ""
	if query != nil {
		q = *query
	}
	queryPtr := i.storeString(q)
	defer i.RemoveValue(queryPtr)

	fw := postcard.NewWriter()
	models.EncodeFilterValueSlice(fw, filters)
	filterPtr := i.storeBytes(fw.Bytes())
	defer i.RemoveValue(filterPtr)

	data, err := i.call(ctx, "get_search_manga_list",
		api.EncodeI32(queryPtr), api.EncodeI32(int32(page)), api.EncodeI32(filterPtr))
	if err != nil {
		return nil, err
	}
	var result models.MangaPageResult
	if err := result.DecodePostcard(postcard.NewReader(data)); err != nil {
		return nil, err
	}
	return &result, nil
}

func (i *Interpreter) GetMangaUpdate(
	ctx context.Context,
	manga models.Manga,
	needsDetails, needsChapters bool,
	onPartial func(models.Manga),
) (*models.Manga, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	w := postcard.NewWriter()
	manga.EncodePostcard(w)
	mangaPtr := i.storeBytes(w.Bytes())
	defer i.RemoveValue(mangaPtr)

	var data []byte
	err := i.withPartialHandler(func(payload []byte) {
		if onPartial == nil {
			return
		}
		var m models.Manga
		if err := m.DecodePostcard(postcard.NewReader(payload)); err == nil {
			onPartial(m)
		}
	}, func() error {
		var callErr error
		data, callErr = i.call(ctx, "get_manga_update",
			api.EncodeI32(mangaPtr), api.EncodeI32(boolToI32(needsDetails)), api.EncodeI32(boolToI32(needsChapters)))
		return callErr
	})
	if err != nil {
		return nil, err
	}

	var result models.Manga
	if err := result.DecodePostcard(postcard.NewReader(data)); err != nil {
		return nil, err
	}
	return &result, nil
}

func (i *Interpreter) GetPageList(ctx context.Context, manga models.Manga, chapter models.Chapter) ([]models.Page, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	manga.Chapters = nil // matches Swift: `newManga.chapters = nil` before encoding

	mw := postcard.NewWriter()
	manga.EncodePostcard(mw)
	mangaPtr := i.storeBytes(mw.Bytes())
	defer i.RemoveValue(mangaPtr)

	cw := postcard.NewWriter()
	chapter.EncodePostcard(cw)
	chapterPtr := i.storeBytes(cw.Bytes())
	defer i.RemoveValue(chapterPtr)

	data, err := i.call(ctx, "get_page_list", api.EncodeI32(mangaPtr), api.EncodeI32(chapterPtr))
	if err != nil {
		return nil, err
	}
	r := postcard.NewReader(data)
	n, err := r.ReadLen()
	if err != nil {
		return nil, err
	}
	pages := make([]models.Page, n)
	for idx := 0; idx < n; idx++ {
		if err := pages[idx].DecodePostcard(r); err != nil {
			return nil, err
		}
	}
	return pages, nil
}

func (i *Interpreter) GetMangaList(ctx context.Context, listing models.Listing, page int) (*models.MangaPageResult, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	w := postcard.NewWriter()
	listing.EncodePostcard(w)
	listingPtr := i.storeBytes(w.Bytes())
	defer i.RemoveValue(listingPtr)

	data, err := i.call(ctx, "get_manga_list", api.EncodeI32(listingPtr), api.EncodeI32(int32(page)))
	if err != nil {
		return nil, err
	}
	var result models.MangaPageResult
	if err := result.DecodePostcard(postcard.NewReader(data)); err != nil {
		return nil, err
	}
	return &result, nil
}

func (i *Interpreter) GetHome(ctx context.Context, onPartial func(models.Home)) (*models.Home, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	var currentHome *models.Home
	var data []byte
	err := i.withPartialHandler(func(payload []byte) {
		var partial models.HomePartialResult
		if err := partial.DecodePostcard(postcard.NewReader(payload)); err != nil {
			return
		}
		switch partial.Kind {
		case models.HomePartialResultKindLayout:
			home := partial.Layout
			home.SetSourceKey(i.sourceKey)
			currentHome = &home
		case models.HomePartialResultKindComponent:
			component := partial.Component
			component.SetSourceKey(i.sourceKey)
			if currentHome != nil {
				replaced := false
				for idx := range currentHome.Components {
					if strPtrEqual(currentHome.Components[idx].Title, component.Title) {
						currentHome.Components[idx] = component
						replaced = true
						break
					}
				}
				if !replaced {
					currentHome.Components = append(currentHome.Components, component)
				}
			} else {
				currentHome = &models.Home{Components: []models.HomeComponent{component}}
			}
		}
		if onPartial != nil && currentHome != nil {
			onPartial(*currentHome)
		}
	}, func() error {
		var callErr error
		data, callErr = i.call(ctx, "get_home")
		return callErr
	})
	if err != nil {
		return nil, err
	}

	var homeResult models.Home
	if err := homeResult.DecodePostcard(postcard.NewReader(data)); err != nil {
		return nil, err
	}
	homeResult.SetSourceKey(i.sourceKey)

	if len(homeResult.Components) == 0 && currentHome != nil {
		return currentHome, nil
	}
	return &homeResult, nil
}

func strPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (i *Interpreter) ProcessPageImage(ctx context.Context, response models.Response, context *models.PageContext) (int32, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	rw := postcard.NewWriter()
	response.EncodePostcard(rw)
	responsePtr := i.storeBytes(rw.Bytes())
	defer i.RemoveValue(responsePtr)

	contextPtr := int32(-1)
	if context != nil {
		cw := postcard.NewWriter()
		cw.WriteLen(len(*context))
		for k, v := range *context {
			cw.WriteString(k)
			cw.WriteString(v)
		}
		contextPtr = i.storeBytes(cw.Bytes())
		defer i.RemoveValue(contextPtr)
	}

	data, err := i.call(ctx, "process_page_image", api.EncodeI32(responsePtr), api.EncodeI32(contextPtr))
	if err != nil {
		return 0, err
	}
	imageRef, err := postcard.NewReader(data).ReadI32()
	if err != nil {
		return 0, err
	}
	return imageRef, nil
}

func (i *Interpreter) GetSearchFilters(ctx context.Context) ([]models.Filter, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	data, err := i.call(ctx, "get_filters")
	if err != nil {
		return nil, err
	}
	return models.DecodeFilterSlice(postcard.NewReader(data))
}

func (i *Interpreter) GetSettings(ctx context.Context) ([]models.Setting, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	data, err := i.call(ctx, "get_settings")
	if err != nil {
		return nil, err
	}
	return models.DecodeSettingSlice(postcard.NewReader(data))
}

func (i *Interpreter) GetListings(ctx context.Context) ([]models.Listing, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	data, err := i.call(ctx, "get_listings")
	if err != nil {
		return nil, err
	}
	r := postcard.NewReader(data)
	n, err := r.ReadLen()
	if err != nil {
		return nil, err
	}
	out := make([]models.Listing, n)
	for idx := 0; idx < n; idx++ {
		if err := out[idx].DecodePostcard(r); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// GetImageRequest mirrors Interpreter.swift's getImageRequest. The guest's
// get_image_request implementation is expected to build its request via
// the net.* imports (getting back a host Store descriptor) and return that
// descriptor, plain-postcard-encoded, as its result — not the request
// itself. We fetch the already-built *host.NetRequest from the Store using
// that descriptor.
func (i *Interpreter) GetImageRequest(ctx context.Context, url string, pageContext *models.PageContext) (*host.NetRequest, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	urlPtr := i.storeEncodedString(url)
	defer i.RemoveValue(urlPtr)

	contextPtr := int32(-1)
	if pageContext != nil {
		cw := postcard.NewWriter()
		cw.WriteLen(len(*pageContext))
		for k, v := range *pageContext {
			cw.WriteString(k)
			cw.WriteString(v)
		}
		contextPtr = i.storeBytes(cw.Bytes())
		defer i.RemoveValue(contextPtr)
	}

	data, err := i.call(ctx, "get_image_request", api.EncodeI32(urlPtr), api.EncodeI32(contextPtr))
	if err != nil {
		return nil, err
	}
	requestPtr, err := postcard.NewReader(data).ReadI32()
	if err != nil {
		return nil, err
	}
	defer i.RemoveValue(requestPtr)

	req, ok := i.Fetch(requestPtr).(*host.NetRequest)
	if !ok {
		return nil, models.ErrMissingResult()
	}
	return req, nil
}

func (i *Interpreter) GetPageDescription(ctx context.Context, page models.Page) (*string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	w := postcard.NewWriter()
	page.EncodePostcard(w)
	pagePtr := i.storeBytes(w.Bytes())
	defer i.RemoveValue(pagePtr)

	data, err := i.call(ctx, "get_page_description", api.EncodeI32(pagePtr))
	if err != nil {
		return nil, err
	}
	s, err := postcard.NewReader(data).ReadString()
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (i *Interpreter) GetAlternateCovers(ctx context.Context, manga models.Manga) ([]string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	w := postcard.NewWriter()
	manga.EncodePostcard(w)
	mangaPtr := i.storeBytes(w.Bytes())
	defer i.RemoveValue(mangaPtr)

	data, err := i.call(ctx, "get_alternate_covers", api.EncodeI32(mangaPtr))
	if err != nil {
		return nil, err
	}
	r := postcard.NewReader(data)
	n, err := r.ReadLen()
	if err != nil {
		return nil, err
	}
	out := make([]string, n)
	for idx := 0; idx < n; idx++ {
		if out[idx], err = r.ReadString(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (i *Interpreter) GetBaseURL(ctx context.Context) (*string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	data, err := i.call(ctx, "get_base_url")
	if err != nil {
		return nil, err
	}
	s, err := postcard.NewReader(data).ReadString()
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// HandleNotification mirrors Interpreter.swift's handleNotification, which
// uniquely discards the guest's return value outright (no handleResult
// buffer decode, not even an error-code check) rather than going through
// the usual result protocol.
func (i *Interpreter) HandleNotification(ctx context.Context, notification string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	ptr := i.storeEncodedString(notification)
	defer i.RemoveValue(ptr)
	fn := i.module.ExportedFunction("handle_notification")
	if fn == nil {
		return models.ErrUnimplemented()
	}
	_, err := fn.Call(ctx, api.EncodeI32(ptr))
	return err
}

func (i *Interpreter) HandleDeepLink(ctx context.Context, url string) (*models.DeepLinkResult, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	ptr := i.storeEncodedString(url)
	defer i.RemoveValue(ptr)
	data, err := i.call(ctx, "handle_deep_link", api.EncodeI32(ptr))
	if err != nil {
		return nil, err
	}
	return models.DecodeOptionalDeepLinkResult(postcard.NewReader(data))
}

func (i *Interpreter) HandleBasicLogin(ctx context.Context, key, username, password string) (bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	keyPtr := i.storeEncodedString(key)
	defer i.RemoveValue(keyPtr)
	userPtr := i.storeEncodedString(username)
	defer i.RemoveValue(userPtr)
	passPtr := i.storeEncodedString(password)
	defer i.RemoveValue(passPtr)

	data, err := i.call(ctx, "handle_basic_login", api.EncodeI32(keyPtr), api.EncodeI32(userPtr), api.EncodeI32(passPtr))
	if err != nil {
		return false, err
	}
	return postcard.NewReader(data).ReadBool()
}

func (i *Interpreter) HandleWebLogin(ctx context.Context, key string, cookies map[string]string) (bool, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	keys := make([]string, 0, len(cookies))
	values := make([]string, 0, len(cookies))
	for k, v := range cookies {
		keys = append(keys, k)
		values = append(values, v)
	}

	keyPtr := i.storeEncodedString(key)
	defer i.RemoveValue(keyPtr)

	kw := postcard.NewWriter()
	writeStringSlice(kw, keys)
	keysPtr := i.storeBytes(kw.Bytes())
	defer i.RemoveValue(keysPtr)

	vw := postcard.NewWriter()
	writeStringSlice(vw, values)
	valuesPtr := i.storeBytes(vw.Bytes())
	defer i.RemoveValue(valuesPtr)

	data, err := i.call(ctx, "handle_web_login", api.EncodeI32(keyPtr), api.EncodeI32(keysPtr), api.EncodeI32(valuesPtr))
	if err != nil {
		return false, err
	}
	return postcard.NewReader(data).ReadBool()
}

func (i *Interpreter) HandleMigration(ctx context.Context, kind models.KeyKind, mangaKey string, chapterKey *string) (string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	mangaKeyPtr := i.storeEncodedString(mangaKey)
	defer i.RemoveValue(mangaKeyPtr)

	chapterKeyPtr := int32(-1)
	if chapterKey != nil {
		chapterKeyPtr = i.storeEncodedString(*chapterKey)
		defer i.RemoveValue(chapterKeyPtr)
	}

	data, err := i.call(ctx, "handle_key_migration",
		api.EncodeI32(int32(kind)), api.EncodeI32(mangaKeyPtr), api.EncodeI32(chapterKeyPtr))
	if err != nil {
		return "", err
	}
	return postcard.NewReader(data).ReadString()
}
