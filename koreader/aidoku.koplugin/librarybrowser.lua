--[[--
Primary Aidoku screen: bookmarked manga (the "library"), plus an entry to
browse installed/available sources for discovery. Tapping a library entry
opens MangaBrowser directly against its cached key -- no re-search needed.
]]

local ConfirmBox = require("ui/widget/confirmbox")
local Menu = require("ui/widget/menu")
local UIManager = require("ui/uimanager")
local T = require("ffi/util").template
local _ = require("gettext")

local LibraryBrowser = Menu:extend{
    title = _("Aidoku"),
}

local BROWSE_SOURCES_TEXT = _("Browse sources…")

function LibraryBrowser:init()
    self.item_table = self:genItemTable()
    Menu.init(self)
end

function LibraryBrowser:genItemTable()
    local item_table = {}
    table.insert(item_table, { text = BROWSE_SOURCES_TEXT, is_browse_entry = true })
    for _, entry in ipairs(self.store:libraryEntries()) do
        table.insert(item_table, { text = entry.title, library_entry = entry })
    end
    return item_table
end

function LibraryBrowser:refresh()
    self.item_table = self:genItemTable()
    self:updateItems()
end

function LibraryBrowser:onMenuSelect(item)
    if item.is_browse_entry then
        local SourcesBrowser = require("sourcesbrowser")
        UIManager:show(SourcesBrowser:new{
            engine = self.engine,
            store = self.store,
            ui = self.ui,
            sources_dir = self.sources_dir,
            downloads_dir = self.downloads_dir,
            repo_url = self.repo_url,
            is_popout = false,
            is_borderless = true,
            title_bar_fm_style = true,
        })
        return true
    end

    local entry = item.library_entry
    local MangaBrowser = require("mangabrowser")
    UIManager:show(MangaBrowser:new{
        engine = self.engine,
        store = self.store,
        ui = self.ui,
        source_path = entry.source_path,
        downloads_dir = self.downloads_dir,
        manga = { Key = entry.manga_key, Title = entry.title },
        is_popout = false,
        is_borderless = true,
        title_bar_fm_style = true,
    })
    return true
end

function LibraryBrowser:onMenuHold(item)
    if item.is_browse_entry then
        return true
    end
    local entry = item.library_entry
    UIManager:show(ConfirmBox:new{
        text = T(_("Remove '%1' from your library?"), entry.title),
        ok_text = _("Remove"),
        ok_callback = function()
            self.store:removeBookmark(entry.source_path, entry.manga_key)
            self:refresh()
        end,
    })
    return true
end

return LibraryBrowser
