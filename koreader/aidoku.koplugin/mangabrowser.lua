--[[--
Shows one manga's chapter list (fetched fresh via the "manga" command, which
returns details + chapters together), lets the user flip chapter sort order
via a title bar button, and downloads a chapter as a CBZ (skipping the
network entirely if it's already downloaded) then opens it. Bookmarking a
manga to the library happens from searchbrowser.lua's result list, not here.

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
local Trapper = require("ui/trapper")
local UIManager = require("ui/uimanager")
local lfs = require("libs/libkoreader-lfs")
local util = require("util")
local T = require("ffi/util").template
local _ = require("gettext")

local MangaBrowser = Menu:extend{}

-- Manga/Chapter Title fields are plain (non-pointer) Go strings, so an
-- absent title decodes as "" rather than null -- and "" is truthy in Lua,
-- so an explicit emptiness check is needed, not just `x or fallback`.
local function mangaLabel(manga)
    if type(manga.Title) == "string" and manga.Title ~= "" then
        return manga.Title
    end
    return manga.Key
end

-- aidoku-run's JSON encodes Go's nil *string/*float32 fields as JSON null,
-- which KOReader's json module decodes as a function sentinel (not Lua nil,
-- since a table can't hold a nil value) -- see the comment on this in
-- wallabag.koplugin/main.lua. So these need an explicit type check rather
-- than a truthiness check.
local function chapterLabel(chapter)
    if type(chapter.Title) == "string" and chapter.Title ~= "" then
        return chapter.Title
    end
    if type(chapter.ChapterNumber) == "number" then
        return T(_("Chapter %1"), chapter.ChapterNumber)
    end
    return chapter.Key
end

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

-- reloadDownloadedKeys fetches the full downloads index once and keeps
-- just this manga's entries, so per-row downloadedPath() lookups are local
-- table reads instead of one subprocess call each -- see the note on this
-- in downloadedPath().
function MangaBrowser:reloadDownloadedKeys()
    local all = self.downloads_engine:list()
    self.downloaded_keys = {}
    if not all then
        return
    end
    for _, entry in ipairs(all) do
        if entry.sourceKey == self.source_key and entry.mangaKey == self.manga.Key then
            self.downloaded_keys[entry.chapterKey] = entry.path
        end
    end
end

function MangaBrowser:reload()
    NetworkMgr:runWhenConnected(function()
        Trapper:wrap(function()
            local updated, err = self.engine:mangaUpdate(self.source_path, self.manga.Key)
            if not updated then
                UIManager:show(InfoMessage:new{ text = T(_("Could not load chapters:\n%1"), err) })
                return
            end
            self.manga = updated
            self.chapters = type(updated.Chapters) == "table" and updated.Chapters or {}
            self:reloadDownloadedKeys()
            self:refresh()
        end)
    end)
end

-- prefetchAhead silently downloads up to store:bufferChapters() upcoming
-- chapters (in reading order, not display sort order) after chapter, so
-- they're likely already local by the time the reader reaches them. Runs
-- via Trapper with a false progress widget (see the note on this in
-- engine.lua's download()), so it doesn't interrupt whatever the user is
-- doing with a visible dialog.
function MangaBrowser:prefetchAhead(chapter)
    local n = self.store:bufferChapters()
    if n <= 0 then
        return
    end
    local upcoming = ChapterOrder.after(self.chapters, chapter.Key, n)
    if #upcoming == 0 then
        return
    end
    Trapper:wrap(function()
        local any_new = false
        for _, c in ipairs(upcoming) do
            if self:downloadedPath(c.Key) == "" then
                local filename = util.getSafeFilename(
                    mangaLabel(self.manga) .. " - " .. chapterLabel(c) .. ".cbz", self.downloads_dir)
                local out_path = self.downloads_dir .. "/" .. filename
                local path = self.engine:download(self.source_path, self.manga.Key, c.Key, out_path, self.downloads_dir, true)
                if path then
                    self.downloaded_keys[c.Key] = path
                    any_new = true
                end
            end
        end
        if any_new then
            self.downloads_engine:prune(self.store:downloadLimitBytes())
            self:refresh()
        end
    end)
end

function MangaBrowser:openLocal(path)
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
        UIManager:show(ConfirmBox:new{
            text = T(_("'%1' is already downloaded. Open it?"), chapterLabel(item.chapter)),
            ok_text = _("Read"),
            ok_callback = function()
                self:prefetchAhead(item.chapter)
                self:openLocal(existing)
            end,
        })
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
