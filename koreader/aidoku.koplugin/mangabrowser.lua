--[[--
Shows one manga's chapter list (fetched fresh via the "manga" command, which
returns details + chapters together), lets the user flip chapter sort order
via a title bar button, and downloads a chapter as a CBZ (skipping the
network entirely if it's already downloaded) then opens it. Bookmarking a
manga to the library happens from searchbrowser.lua's result list, not here.
]]

local ButtonDialog = require("ui/widget/buttondialog")
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
-- downloaded and the file still exists, self-healing the index (removing
-- the stale entry) if the file was deleted out from under it.
function MangaBrowser:downloadedPath(chapter_key)
    local path = self.store:downloadedPath(self.source_path, self.manga.Key, chapter_key)
    if not path then
        return nil
    end
    if lfs.attributes(path, "mode") ~= "file" then
        self.store:removeDownload(self.source_path, self.manga.Key, chapter_key)
        return nil
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
    for _, chapter in ipairs(ordered) do
        local label = chapterLabel(chapter)
        if self:downloadedPath(chapter.Key) then
            label = "✓ " .. label
        end
        table.insert(item_table, { text = label, chapter = chapter })
    end
    return item_table
end

function MangaBrowser:refresh()
    self.item_table = self:genItemTable()
    self:updateItems()
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
            self:refresh()
        end)
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

            local path, err = self.engine:download(self.source_path, self.manga.Key, chapter.Key, out_path)
            if not path then
                UIManager:show(InfoMessage:new{ text = T(_("Download failed:\n%1"), err) })
                return
            end
            self.store:recordDownload(self.source_path, self.manga.Key, chapter.Key, path,
                mangaLabel(self.manga), chapterLabel(chapter))
            self:refresh()
            UIManager:show(ConfirmBox:new{
                text = T(_("Downloaded to:\n%1\n\nRead now?"), path),
                ok_text = _("Read now"),
                ok_callback = function() self:openLocal(path) end,
            })
        end)
    end)
end

function MangaBrowser:onMenuSelect(item)
    local existing = self:downloadedPath(item.chapter.Key)
    if existing then
        UIManager:show(ConfirmBox:new{
            text = T(_("'%1' is already downloaded. Open it?"), chapterLabel(item.chapter)),
            ok_text = _("Read"),
            ok_callback = function() self:openLocal(existing) end,
        })
        return true
    end

    self:downloadAndOpen(item.chapter)
    return true
end

function MangaBrowser:onMenuHold(item)
    local existing = self:downloadedPath(item.chapter.Key)
    if not existing then
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
                    self.store:removeDownload(self.source_path, self.manga.Key, item.chapter.Key)
                    self:downloadAndOpen(item.chapter)
                end,
            }},
            {{
                text = _("Remove download"),
                callback = function()
                    UIManager:close(dialog)
                    self.store:removeDownload(self.source_path, self.manga.Key, item.chapter.Key)
                    self:refresh()
                end,
            }},
        },
    }
    UIManager:show(dialog)
    return true
end

return MangaBrowser
