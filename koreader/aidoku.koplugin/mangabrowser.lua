--[[--
Shows one manga's chapter list, lets the user flip chapter sort order via a
title bar button, and downloads a chapter as a CBZ (skipping the network
entirely if it's already downloaded) then opens it. Bookmarking a manga to
the library happens from searchbrowser.lua's result list, not here.

reload() renders the local downloads index first (see the note there) and
only opportunistically refreshes from the network's "manga" command (details
+ chapters together) when already connected, so already-downloaded chapters
stay browsable/openable fully offline.

Requires both source_path (the installed .aix's path, used for every
content operation -- search/mangaUpdate/download) and source_key (the
source's stable manifest ID, used for every downloads-index operation --
see the downloads Go package doc for why those are kept separate). Callers
that only have one of the two (e.g. librarybrowser.lua's bookmark entries,
which store source_key and must re-resolve source_path via
installedsources.lua's findByKey before opening this) must resolve the
other first.
]]

local ButtonDialog = require("ui/widget/buttondialog")
local ChapterOrder = require("chapterorder")
local ConfirmBox = require("ui/widget/confirmbox")
local InfoMessage = require("ui/widget/infomessage")
local Menu = require("ui/widget/menu")
local NetworkMgr = require("ui/network/manager")
local Prefetch = require("prefetch")
local Trapper = require("ui/trapper")
local UIManager = require("ui/uimanager")
local lfs = require("libs/libkoreader-lfs")
local util = require("util")
local T = require("ffi/util").template
local _ = require("gettext")

local MangaBrowser = Menu:extend{}

local mangaLabel = Prefetch.mangaLabel
local chapterLabel = Prefetch.chapterLabel

function MangaBrowser:init()
    self.title = mangaLabel(self.manga)
    self.chapters = {}
    self.downloaded_keys = {} -- chapter Key -> local path, refreshed by reload()
    self.item_table = {}
    -- Sort-order toggle lives on the title bar's left button since it
    -- applies to the whole list, not one row -- see Menu:onLeftButtonTap
    -- in frontend/ui/widget/menu.lua. There's no dedicated "sort" icon in
    -- KOReader's icon set, so move.up/move.down (already directional)
    -- stand in, swapped each time the order flips.
    self.title_bar_left_icon = self:sortIcon()
    Menu.init(self)
    -- See the same note in repobrowser.lua's init(): defer past the
    -- caller's own UIManager:show(self), since reload()'s progress widget
    -- would otherwise show (and get buried) first.
    UIManager:nextTick(function() self:reload() end)
end

function MangaBrowser:sortIcon()
    return self.store:sortOrder() == "asc" and "move.up" or "move.down"
end

function MangaBrowser:onLeftButtonTap()
    self.store:setSortOrder(self.store:sortOrder() == "asc" and "desc" or "asc")
    self:refresh()
    UIManager:show(InfoMessage:new{
        text = self.store:sortOrder() == "asc" and _("Sorted: oldest first") or _("Sorted: newest first"),
        timeout = 1.5,
    })
end

-- downloadedPath returns the local CBZ path for chapter_key if it's
-- downloaded and the file still exists, or "" otherwise. Reads
-- self.downloaded_keys (an in-memory snapshot refreshed by reload() and
-- kept up to date locally after each download/removal here) rather than
-- querying the downloads index per chapter -- this is called once per row
-- while building the chapter list, and a manga can have dozens of
-- chapters, so a subprocess call per row is not an option. The lfs check
-- is a cheap local stat (no subprocess) so it's fine to do per row; a
-- stale entry (file deleted out from under us) is just treated as "not
-- downloaded" here rather than cleaned up, since that too would need a
-- subprocess call mid-render -- record() overwrites it on the next real
-- download regardless.
function MangaBrowser:downloadedPath(chapter_key)
    local path = self.downloaded_keys[chapter_key]
    if not path or path == "" then
        return ""
    end
    if lfs.attributes(path, "mode") ~= "file" then
        return ""
    end
    return path
end

function MangaBrowser:genItemTable()
    local item_table = {}
    local sort_order = self.store:sortOrder()
    local ordered = self.chapters
    if sort_order == "asc" then
        ordered = {}
        for i = #self.chapters, 1, -1 do
            table.insert(ordered, self.chapters[i])
        end
    end
    for _idx, chapter in ipairs(ordered) do
        local mandatory_parts = {}
        if self.store:isChapterRead(self.source_key, self.manga.Key, chapter.Key) then
            table.insert(mandatory_parts, _("Read"))
        end
        if self:downloadedPath(chapter.Key) ~= "" then
            table.insert(mandatory_parts, "✓")
        end
        table.insert(item_table, {
            text = chapterLabel(chapter),
            mandatory = table.concat(mandatory_parts, " "),
            chapter = chapter,
        })
    end
    return item_table
end

function MangaBrowser:refresh()
    self.item_table = self:genItemTable()
    self:updateItems()
end

-- reloadDownloadedKeys fetches the full downloads index (unless a
-- previously-fetched list is passed in, to avoid a second subprocess call
-- when reload() already has one) and keeps just this manga's entries, so
-- per-row downloadedPath() lookups are local table reads instead of one
-- subprocess call each -- see the note on this in downloadedPath(). Returns
-- the full (unfiltered) list, for callers that also need it (reload()'s
-- offline chapter-list fallback).
function MangaBrowser:reloadDownloadedKeys(all)
    all = all or self.downloads_engine:list()
    self.downloaded_keys = {}
    if not all then
        return all
    end
    for _, entry in ipairs(all) do
        if entry.sourceKey == self.source_key and entry.mangaKey == self.manga.Key then
            self.downloaded_keys[entry.chapterKey] = entry.path
        end
    end
    return all
end

-- reload() always shows what's already downloaded first, straight from the
-- local downloads index -- no network involved -- so this manga's
-- downloaded chapters stay browsable/openable even fully offline. It then
-- opportunistically refreshes from the network (fetching the real chapter
-- list, titles, and any chapters not yet downloaded) only if already
-- connected; if not, it skips straight past that rather than popping a
-- "connect to network?" prompt, since the point is to let already-downloaded
-- content just work without a network fuss.
function MangaBrowser:reload()
    Trapper:wrap(function()
        local all = self:reloadDownloadedKeys()
        if #self.chapters == 0 then
            self.chapters = ChapterOrder.fromEntries(all, self.source_key, self.manga.Key)
        end
        self:refresh()

        if not NetworkMgr:isConnected() then
            return
        end

        local updated, err = self.engine:mangaUpdate(self.source_path, self.manga.Key)
        if not updated then
            if #self.chapters == 0 then
                UIManager:show(InfoMessage:new{ text = T(_("Could not load chapters:\n%1"), err) })
            end
            return
        end
        self.manga = updated
        self.chapters = type(updated.Chapters) == "table" and updated.Chapters or {}
        self:reloadDownloadedKeys()
        self:refresh()
    end)
end

-- prefetchAhead silently downloads up to store:bufferChapters() upcoming
-- chapters (in reading order, not display sort order) after chapter, so
-- they're likely already local by the time the reader reaches them. See
-- prefetch.lua -- shared with nextchapter.lua's end-of-book auto-advance,
-- so the buffer keeps refilling as the user reads through, not just on the
-- first chapter opened from this list.
function MangaBrowser:prefetchAhead(chapter)
    Prefetch.ahead({
        engine = self.engine,
        downloads_engine = self.downloads_engine,
        store = self.store,
        downloads_dir = self.downloads_dir,
        source_path = self.source_path,
        source_key = self.source_key,
        manga = self.manga,
    }, self.chapters, chapter.Key,
    function(c, path)
        self.downloaded_keys[c.Key] = path
    end,
    function(any_new)
        if any_new then
            self:refresh()
        end
    end)
end

function MangaBrowser:openLocal(path)
    self.store:markLastRead(self.source_key, self.manga.Key)
    UIManager:close(self)
    if self.ui.document then
        self.ui:switchDocument(path)
    else
        self.ui:openFile(path)
    end
end

function MangaBrowser:downloadAndOpen(chapter)
    NetworkMgr:runWhenConnected(function()
        Trapper:wrap(function()
            local filename = mangaLabel(self.manga) .. " - " .. chapterLabel(chapter) .. ".cbz"
            filename = util.getSafeFilename(filename, self.downloads_dir)
            local out_path = self.downloads_dir .. "/" .. filename

            local path, err = self.engine:download(self.source_path, self.manga.Key, chapter.Key, out_path, self.downloads_dir)
            if not path then
                UIManager:show(InfoMessage:new{ text = T(_("Download failed:\n%1"), err) })
                return
            end
            self.downloaded_keys[chapter.Key] = path
            self.downloads_engine:prune(self.store:downloadLimitBytes())
            self:refresh()
            self:prefetchAhead(chapter)
            self:openLocal(path)
        end)
    end)
end

function MangaBrowser:onMenuSelect(item)
    local existing = self:downloadedPath(item.chapter.Key)
    if existing ~= "" then
        self:openLocal(existing)
        self:prefetchAhead(item.chapter)
        return true
    end

    self:downloadAndOpen(item.chapter)
    return true
end

function MangaBrowser:onMenuHold(item)
    local existing = self:downloadedPath(item.chapter.Key)
    if existing == "" then
        return true
    end

    local dialog
    dialog = ButtonDialog:new{
        title = chapterLabel(item.chapter),
        buttons = {
            {{
                text = _("Re-download"),
                callback = function()
                    UIManager:close(dialog)
                    Trapper:wrap(function()
                        self.downloads_engine:remove(self.source_key, self.manga.Key, item.chapter.Key)
                        self.downloaded_keys[item.chapter.Key] = nil
                        self:downloadAndOpen(item.chapter)
                    end)
                end,
            }},
            {{
                text = _("Remove download"),
                callback = function()
                    UIManager:close(dialog)
                    Trapper:wrap(function()
                        self.downloads_engine:remove(self.source_key, self.manga.Key, item.chapter.Key)
                        self.downloaded_keys[item.chapter.Key] = nil
                        self:refresh()
                    end)
                end,
            }},
        },
    }
    UIManager:show(dialog)
    return true
end

return MangaBrowser
