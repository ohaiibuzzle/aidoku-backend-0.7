--[[--
End-of-book auto-advance: when the reader fires EndOfBook for a document
that's one of our downloaded chapters (per downloads_engine:byPath), looks
up the manga's current chapter list, and per the user's next_chapter_mode
setting either offers or silently proceeds to download (if needed) and open
the next chapter in reading order (see chapterorder.lua).
]]

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

    NetworkMgr:runWhenConnected(function()
        Trapper:wrap(function()
            local entry, lookup_err = ctx.downloads_engine:byPath(file_path)
            if not entry then
                if lookup_err then
                    UIManager:show(InfoMessage:new{ text = T(_("Could not check for next chapter:\n%1"), lookup_err) })
                end
                return
            end

            -- entry.sourcePath is only a stale hint (see the downloads Go
            -- package doc) -- resolve the source's current installed path
            -- from its key before using it to load the source.
            local found = InstalledSources.findByKey(ctx.sources_dir, ctx.engine, entry.sourceKey)
            if not found then
                UIManager:show(InfoMessage:new{ text = _("Could not check for next chapter: this source is no longer installed.") })
                return
            end
            local source_path = found.path

            local updated, err = ctx.engine:mangaUpdate(source_path, entry.mangaKey)
            if not updated then
                UIManager:show(InfoMessage:new{ text = T(_("Could not check for next chapter:\n%1"), err) })
                return
            end
            local chapters = type(updated.Chapters) == "table" and updated.Chapters or {}
            local upcoming = ChapterOrder.after(chapters, entry.chapterKey, 1)
            local next_chapter = upcoming[1]
            if not next_chapter then
                UIManager:show(InfoMessage:new{ text = _("You're all caught up -- no next chapter yet."), timeout = 2 })
                return
            end

            -- Re-primes the prefetch buffer on every auto-advance, not just
            -- the first chapter opened from mangabrowser.lua's list --
            -- otherwise the buffer never refills once the user reads past
            -- what was originally prefetched. See prefetch.lua.
            local prefetch_ctx = {
                engine = ctx.engine,
                downloads_engine = ctx.downloads_engine,
                store = ctx.store,
                downloads_dir = ctx.downloads_dir,
                source_path = source_path,
                source_key = entry.sourceKey,
                manga = updated,
            }

            local function fetchAndOpen()
                local existing = ctx.downloads_engine:path(entry.sourceKey, entry.mangaKey, next_chapter.Key)
                if existing ~= "" then
                    ctx.open_callback(existing)
                    Prefetch.ahead(prefetch_ctx, chapters, next_chapter.Key)
                    return
                end
                local filename = util.getSafeFilename(
                    mangaLabel(updated) .. " - " .. chapterLabel(next_chapter) .. ".cbz", ctx.downloads_dir)
                local out_path = ctx.downloads_dir .. "/" .. filename
                local path, dl_err = ctx.engine:download(source_path, entry.mangaKey, next_chapter.Key, out_path, ctx.downloads_dir)
                if not path then
                    UIManager:show(InfoMessage:new{ text = T(_("Download failed:\n%1"), dl_err) })
                    return
                end
                ctx.downloads_engine:prune(ctx.store:downloadLimitBytes())
                ctx.open_callback(path)
                Prefetch.ahead(prefetch_ctx, chapters, next_chapter.Key)
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
    end)
    return true
end

return NextChapter
