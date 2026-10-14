--[[--
End-of-book auto-advance: when the reader fires EndOfBook for a document
that's one of our downloaded chapters (per downloads_engine:byPath), finds
the next chapter in reading order -- from the full chapter list when it's
cached or we're already connected, else from the downloads index alone
(fully offline, see chapterorder.lua's fromEntries) -- and opens it from disk
if downloaded, or downloads it first if connected. Per the user's
next_chapter_mode setting, either offers or silently proceeds.
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
local _ = require("aidoku_l10n")

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
    -- downloads_engine:byPath() reads index.db directly (lua-ljsqlite3, no
    -- subprocess call), but a cheap path-prefix check still avoids that
    -- SQLite lookup entirely for the common case of finishing some other,
    -- unrelated book: only chapters we downloaded ever live under
    -- downloads_dir.
    if file_path:sub(1, #ctx.downloads_dir) ~= ctx.downloads_dir then
        return false
    end
    if ctx.store:nextChapterMode() == "off" then
        return false
    end

    -- Looked up before claiming the event: a file under downloads_dir that
    -- the index doesn't know (copied in by hand, or a busy/damaged index.db)
    -- must fall through to KOReader's own end-of-book popup rather than get
    -- no end-of-book handling at all. byPath is a synchronous SQLite read.
    local entry = ctx.downloads_engine:byPath(file_path)
    if not entry then
        return false
    end

    Trapper:wrap(function()
        -- entry.sourcePath is only a stale hint (see the downloads Go
        -- package doc) -- the source's current installed path is resolved
        -- from its key, but only once something actually needs it (prefetch
        -- or the network fallback): findByKey runs one aidoku-run per
        -- installed source, and opening an already-downloaded next chapter
        -- needs no source at all -- it must keep working even if the source
        -- was since uninstalled.
        local source_path, source_resolved
        local function resolveSource()
            if not source_resolved then
                source_resolved = true
                local found = InstalledSources.findByKey(ctx.sources_dir, ctx.engine, entry.sourceKey)
                source_path = found and found.path
            end
            return source_path
        end

        -- manga/prefetch_ctx start out built from just the downloads-index
        -- entry (no network fetch needed for this much). The network
        -- fallback further down replaces prefetch_ctx.manga with the fully
        -- fetched manga once/if it fetches one.
        local manga = { Key = entry.mangaKey, Title = entry.mangaTitle }
        local prefetch_ctx = {
            engine = ctx.engine,
            downloads_engine = ctx.downloads_engine,
            store = ctx.store,
            downloads_dir = ctx.downloads_dir,
            source_key = entry.sourceKey,
            manga = manga,
        }
        local function prefetchAfter(chapters, chapter_key)
            -- Deferred to the next tick so its subprocess calls don't steal
            -- the reader's first page-turn taps (same as mangabrowser.lua's
            -- downloadAndOpen()).
            -- Wrapped: resolveSource() may run aidoku-run (manifest per
            -- installed source), which outside a Trapper coroutine falls back
            -- to a blocking io.popen and freezes the reader.
            UIManager:nextTick(function()
                if ctx.store:effectiveBufferChapters() <= 0 then
                    return
                end
                Trapper:wrap(function()
                    if resolveSource() then
                        prefetch_ctx.source_path = source_path
                        Prefetch.ahead(prefetch_ctx, chapters, chapter_key)
                    end
                end)
            end)
        end
        local function openDownloaded(path)
            ctx.open_callback(path)
            ctx.store:markLastRead(entry.sourceKey, entry.mangaKey)
            if ctx.store:isEphemeralMode() then
                ctx.downloads_engine:removeAllExcept(path)
            end
        end

        -- Reading order comes from the full chapter list whenever we have
        -- it: ChapterCache (usually primed by mangabrowser.lua's reload()),
        -- else a fetch if already connected. The downloads index only knows
        -- downloaded chapters, so ordering by it alone silently skipped any
        -- chapter in between that wasn't downloaded (a failed prefetch, a
        -- hand-picked download) -- confirmed on-device: with chapters 1 and
        -- 3 downloaded, finishing 1 offered 3. It's still the fallback when
        -- offline, which is the whole point of local-first.
        local updated = ChapterCache.get(entry.sourceKey, entry.mangaKey)
        local update_err
        if not updated and NetworkMgr:isConnected() and resolveSource() then
            updated, update_err = ctx.engine:mangaUpdate(source_path, entry.mangaKey)
            if updated then
                ChapterCache.set(entry.sourceKey, entry.mangaKey, updated)
            end
        end
        local function is_local(c)
            return ctx.downloads_engine:path(entry.sourceKey, entry.mangaKey, c.Key) ~= ""
        end
        local chapters, next_chapter
        if updated and type(updated.Chapters) == "table" then
            chapters = updated.Chapters
            next_chapter = ChapterOrder.after(chapters, entry.chapterKey, 1, is_local)[1]
        end
        if not next_chapter then
            -- Offline/no list, or the list lacks this chapter's key (source
            -- dropped or re-keyed it): the downloads index can still know
            -- what follows. It's a subset of the full list, so this can't
            -- skip ahead of a true last chapter.
            local local_chapters = ChapterOrder.fromEntries(ctx.downloads_engine:list(), entry.sourceKey, entry.mangaKey)
            next_chapter = ChapterOrder.after(local_chapters, entry.chapterKey, 1, is_local)[1]
            if next_chapter or not chapters then
                chapters = local_chapters
            end
        end
        if not next_chapter then
            UIManager:show(InfoMessage:new{ text = _("No next chapter yet."), timeout = 2 })
            return
        end

        -- Re-resolve the path rather than trusting any list: covers a stale
        -- index row whose file was deleted out from under it -- see the same
        -- note in mangabrowser.lua's downloadedPath().
        local existing = ctx.downloads_engine:path(entry.sourceKey, entry.mangaKey, next_chapter.Key)
        if existing ~= "" then
            local function openLocalNext()
                openDownloaded(existing)
                prefetchAfter(chapters, next_chapter.Key)
            end
            if ctx.store:nextChapterMode() == "auto" then
                openLocalNext()
            else -- "ask"
                UIManager:show(ConfirmBox:new{
                    text = T(_("Continue to '%1'?"), chapterLabel(next_chapter)),
                    ok_text = _("Continue"),
                    ok_callback = openLocalNext,
                })
            end
            return
        end

        -- The next chapter isn't downloaded. Fetching it only makes sense if
        -- we're already connected -- popping a "connect to network?" prompt
        -- in the middle of reading would be disruptive.
        if not NetworkMgr:isConnected() then
            UIManager:show(InfoMessage:new{ text = _("Offline, and the next chapter isn't downloaded."), timeout = 2 })
            return
        end
        if not resolveSource() then
            UIManager:show(InfoMessage:new{ text = _("Source no longer installed.") })
            return
        end
        if not updated then
            -- Only reachable via a stale local row: the fetch above failed.
            UIManager:show(InfoMessage:new{ text = T(_("Could not check for next chapter:\n%1"), update_err) })
            return
        end
        prefetch_ctx.manga = updated

        local function fetchAndOpen()
            Trapper:wrap(function()
                local existing = ctx.downloads_engine:path(entry.sourceKey, entry.mangaKey, next_chapter.Key)
                if existing ~= "" then
                    openDownloaded(existing)
                    prefetchAfter(chapters, next_chapter.Key)
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
                -- See the same note on skipping prune() under Ephemeral Mode
                -- (and passing path as keep_path even when it isn't) in
                -- mangabrowser.lua's downloadAndOpen.
                if not ctx.store:isEphemeralMode() then
                    ctx.downloads_engine:prune(ctx.store:downloadLimitBytes(), path)
                end
                openDownloaded(path)
                prefetchAfter(chapters, next_chapter.Key)
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
