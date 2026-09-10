--[[--
End-of-book auto-advance: when the reader fires EndOfBook for a document
that's one of our downloaded chapters (per downloads_engine:byPath), tries
the downloads index first for a next chapter already downloaded (fully
offline, see chapterorder.lua's fromEntries), and only falls back to the
network's "manga" command -- and only if already connected -- when nothing
downloaded follows. Per the user's next_chapter_mode setting, either offers
or silently proceeds to open (or, for the network fallback, download then
open) the next chapter in reading order.
]]

local ChapterCache = require("chaptercache")
local ChapterOrder = require("chapterorder")
local ConfirmBox = require("ui/widget/confirmbox")
local InfoMessage = require("ui/widget/infomessage")
local InstalledSources = require("installedsources")
local NetworkMgr = require("ui/network/manager")
local Prefetch = require("prefetch")
local Trapper = require("ui/trapper")
local UIManager = require("ui/uimanager")
local util = require("util")
local T = require("ffi/util").template
local _ = require("gettext")

local NextChapter = {}

local mangaLabel = Prefetch.mangaLabel
local chapterLabel = Prefetch.chapterLabel

-- handle is called from Aidoku:onEndOfBook() with:
--   engine, downloads_engine, store, downloads_dir, sources_dir -- same as
--   elsewhere in the plugin
--   file_path -- the document that just ended
--   open_callback(path) -- how to actually switch the reader to a new file
-- Returns true if file_path was one of ours and this took over (so the
-- caller should suppress KOReader's own end-of-book popup), false
-- otherwise.
function NextChapter.handle(ctx, file_path)
    -- downloads_engine:byPath() is a subprocess call (the index moved to
    -- the Go backend), and this function must return synchronously to its
    -- caller -- there's no way to await a subprocess result before
    -- returning. Only chapters we downloaded ever live under downloads_dir,
    -- so a cheap path-prefix check tells us up front whether this document
    -- could possibly be ours, without touching the index at all for the
    -- common case of finishing some other, unrelated book.
    if file_path:sub(1, #ctx.downloads_dir) ~= ctx.downloads_dir then
        return false
    end
    if ctx.store:nextChapterMode() == "off" then
        return false
    end

    Trapper:wrap(function()
        local entry, lookup_err = ctx.downloads_engine:byPath(file_path)
        if not entry then
            if lookup_err then
                UIManager:show(InfoMessage:new{ text = T(_("Could not check for next chapter:\n%1"), lookup_err) })
            end
            return
        end

        -- entry.sourcePath is only a stale hint (see the downloads Go
        -- package doc) -- resolve the source's current installed path from
        -- its key before using it to load the source. Both this and the
        -- downloads-index lookups below are purely local (no network), so
        -- they always run regardless of connectivity.
        local found = InstalledSources.findByKey(ctx.sources_dir, ctx.engine, entry.sourceKey)
        if not found then
            UIManager:show(InfoMessage:new{ text = _("Could not check for next chapter: this source is no longer installed.") })
            return
        end
        local source_path = found.path

        -- manga/prefetch_ctx start out built from just the downloads-index
        -- entry (no network fetch needed for this much) -- good enough for
        -- the locally-downloaded-next-chapter path below. The network
        -- fallback further down replaces prefetch_ctx.manga with the fully
        -- fetched manga once/if it fetches one.
        local manga = { Key = entry.mangaKey, Title = entry.mangaTitle }
        local prefetch_ctx = {
            engine = ctx.engine,
            downloads_engine = ctx.downloads_engine,
            store = ctx.store,
            downloads_dir = ctx.downloads_dir,
            source_path = source_path,
            source_key = entry.sourceKey,
            manga = manga,
        }

        -- Try a chapter already downloaded ahead of this one first -- fully
        -- offline, no network round-trip -- so advancing through chapters
        -- downloaded ahead of time (via mangabrowser.lua's prefetch) works
        -- even fully offline or on a spotty connection.
        local all = ctx.downloads_engine:list()
        local local_chapters = ChapterOrder.fromEntries(all, entry.sourceKey, entry.mangaKey)
        local local_next = ChapterOrder.after(local_chapters, entry.chapterKey, 1)[1]

        -- local_chapters only contains already-downloaded chapters, so
        -- Prefetch.ahead against it can never see past the newest local one
        -- -- a guaranteed no-op that silently killed prefetch on every other
        -- transition (confirmed on-device). Use ChapterCache's full network
        -- list for Prefetch.ahead when available; fall back to
        -- local_chapters only on a cache miss.
        local cached = ChapterCache.get(entry.sourceKey, entry.mangaKey)
        local prefetch_chapters = (cached and type(cached.Chapters) == "table") and cached.Chapters or local_chapters
        if local_next then
            -- Re-resolve the path rather than trusting the entry we just
            -- filtered from: covers the (rare) case of a stale index row
            -- whose file was deleted out from under it -- see the same note
            -- in mangabrowser.lua's downloadedPath().
            local existing = ctx.downloads_engine:path(entry.sourceKey, entry.mangaKey, local_next.Key)
            if existing ~= "" then
                local function openLocalNext()
                    ctx.open_callback(existing)
                    if ctx.store:isEphemeralMode() then
                        ctx.downloads_engine:removeAllExcept(existing)
                    end
                    -- Re-primes the prefetch buffer on every auto-advance,
                    -- not just the first chapter opened. Deferred to the
                    -- next tick so its subprocess calls don't steal the
                    -- reader's first page-turn taps (same as
                    -- mangabrowser.lua's downloadAndOpen()).
                    UIManager:nextTick(function()
                        Prefetch.ahead(prefetch_ctx, prefetch_chapters, local_next.Key)
                    end)
                end
                if ctx.store:nextChapterMode() == "auto" then
                    openLocalNext()
                else -- "ask"
                    UIManager:show(ConfirmBox:new{
                        text = T(_("Continue to '%1'?"), chapterLabel(local_next)),
                        ok_text = _("Continue"),
                        ok_callback = openLocalNext,
                    })
                end
                return
            end
        end

        -- No downloaded chapter follows this one. Falling back to the
        -- network to check for something newer only makes sense if we're
        -- already connected -- popping a "connect to network?" prompt in
        -- the middle of reading would be disruptive, and the whole point of
        -- the local-first check above is to let already-downloaded content
        -- just work without a network fuss.
        if not NetworkMgr:isConnected() then
            UIManager:show(InfoMessage:new{ text = _("No downloaded next chapter, and you're offline."), timeout = 2 })
            return
        end

        -- ChapterCache first -- see chaptercache.lua and the note on
        -- mangabrowser.lua's reload(), which is usually what populated it
        -- for this manga already, since opening a chapter list goes through
        -- there before any chapter can be read at all. On a miss (e.g. this
        -- manga was only ever advanced through via downloaded chapters
        -- before now), fall back to the network fetch as before.
        local updated = ChapterCache.get(entry.sourceKey, entry.mangaKey)
        if not updated then
            local err
            updated, err = ctx.engine:mangaUpdate(source_path, entry.mangaKey)
            if not updated then
                UIManager:show(InfoMessage:new{ text = T(_("Could not check for next chapter:\n%1"), err) })
                return
            end
            ChapterCache.set(entry.sourceKey, entry.mangaKey, updated)
        end
        local chapters = type(updated.Chapters) == "table" and updated.Chapters or {}
        local upcoming = ChapterOrder.after(chapters, entry.chapterKey, 1)
        local next_chapter = upcoming[1]
        if not next_chapter then
            UIManager:show(InfoMessage:new{ text = _("You're all caught up -- no next chapter yet."), timeout = 2 })
            return
        end
        prefetch_ctx.manga = updated

        local function fetchAndOpen()
            local existing = ctx.downloads_engine:path(entry.sourceKey, entry.mangaKey, next_chapter.Key)
            if existing ~= "" then
                ctx.open_callback(existing)
                if ctx.store:isEphemeralMode() then
                    ctx.downloads_engine:removeAllExcept(existing)
                end
                -- See the deferral note on openLocalNext() above.
                UIManager:nextTick(function()
                    Prefetch.ahead(prefetch_ctx, chapters, next_chapter.Key)
                end)
                return
            end
            local chapter_filename = util.getSafeFilename(
                chapterLabel(next_chapter) .. ".cbz", ctx.downloads_dir)
            local out_path = ctx.downloads_dir .. "/" .. chapter_filename
            local manga_dir_name = mangaLabel(updated) .. " [" .. entry.sourceKey .. "]"
            local path, dl_err = ctx.engine:download(
                source_path, entry.mangaKey, next_chapter.Key, out_path, ctx.downloads_dir, nil, nil, manga_dir_name)
            if not path then
                UIManager:show(InfoMessage:new{ text = T(_("Download failed:\n%1"), dl_err) })
                return
            end
            -- See the same note on skipping prune() under Ephemeral Mode in
            -- mangabrowser.lua's downloadAndOpen.
            if not ctx.store:isEphemeralMode() then
                ctx.downloads_engine:prune(ctx.store:downloadLimitBytes())
            end
            ctx.open_callback(path)
            if ctx.store:isEphemeralMode() then
                ctx.downloads_engine:removeAllExcept(path)
            end
            -- See the deferral note on openLocalNext() above.
            UIManager:nextTick(function()
                Prefetch.ahead(prefetch_ctx, chapters, next_chapter.Key)
            end)
        end

        if ctx.store:nextChapterMode() == "auto" then
            fetchAndOpen()
        else -- "ask"
            UIManager:show(ConfirmBox:new{
                text = T(_("Continue to '%1'?"), chapterLabel(next_chapter)),
                ok_text = _("Continue"),
                ok_callback = fetchAndOpen,
            })
        end
    end)
    return true
end

return NextChapter
