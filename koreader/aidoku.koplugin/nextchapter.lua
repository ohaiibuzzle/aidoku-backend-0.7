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
local NetworkMgr = require("ui/network/manager")
local Trapper = require("ui/trapper")
local UIManager = require("ui/uimanager")
local util = require("util")
local T = require("ffi/util").template
local _ = require("gettext")

local NextChapter = {}

local function mangaLabel(manga)
    if type(manga.Title) == "string" and manga.Title ~= "" then
        return manga.Title
    end
    return manga.Key
end

local function chapterLabel(chapter)
    if type(chapter.Title) == "string" and chapter.Title ~= "" then
        return chapter.Title
    end
    if ChapterOrder.number(chapter) then
        return T(_("Chapter %1"), chapter.ChapterNumber)
    end
    return chapter.Key
end

-- handle is called from Aidoku:onEndOfBook() with:
--   engine, downloads_engine, store, downloads_dir -- same as elsewhere in
--   the plugin
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

            local updated, err = ctx.engine:mangaUpdate(entry.sourcePath, entry.mangaKey)
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

            local function fetchAndOpen()
                local existing = ctx.downloads_engine:path(entry.sourcePath, entry.mangaKey, next_chapter.Key)
                if existing ~= "" then
                    ctx.open_callback(existing)
                    return
                end
                local filename = util.getSafeFilename(
                    mangaLabel(updated) .. " - " .. chapterLabel(next_chapter) .. ".cbz", ctx.downloads_dir)
                local out_path = ctx.downloads_dir .. "/" .. filename
                local path, dl_err = ctx.engine:download(entry.sourcePath, entry.mangaKey, next_chapter.Key, out_path, ctx.downloads_dir)
                if not path then
                    UIManager:show(InfoMessage:new{ text = T(_("Download failed:\n%1"), dl_err) })
                    return
                end
                ctx.downloads_engine:prune(ctx.store:downloadLimitBytes())
                ctx.open_callback(path)
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
