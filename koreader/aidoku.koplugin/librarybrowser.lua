--[[--
Primary Aidoku screen: just the user's bookmarked manga. Source browsing,
installs, downloads management, and preferences all live behind the title
bar's settings button (see settingsbrowser.lua) so this list stays purely
the library. Tapping an entry opens MangaBrowser directly against its
cached key -- no re-search needed.
]]

local ConfirmBox = require("ui/widget/confirmbox")
local Menu = require("ui/widget/menu")
local UIManager = require("ui/uimanager")
local T = require("ffi/util").template
local _ = require("gettext")

local LibraryBrowser = Menu:extend{
    title = _("Aidoku"),
    title_bar_left_icon = "appbar.settings",
}

function LibraryBrowser:init()
    self.item_table = self:genItemTable()
    Menu.init(self)
end

function LibraryBrowser:genItemTable()
    local item_table = {}
    for _, entry in ipairs(self.store:libraryEntries()) do
        table.insert(item_table, { text = entry.title, library_entry = entry })
    end
    if #item_table == 0 then
        table.insert(item_table, {
            text = _("Your library is empty. Open settings (top left) to browse sources and search for manga."),
            dim = true,
            is_placeholder = true,
        })
    end
    return item_table
end

function LibraryBrowser:refresh()
    self.item_table = self:genItemTable()
    self:updateItems()
end

function LibraryBrowser:onLeftButtonTap()
    local SettingsBrowser = require("settingsbrowser")
    UIManager:show(SettingsBrowser:new{
        engine = self.engine,
        store = self.store,
        downloads_engine = self.downloads_engine,
        ui = self.ui,
        sources_dir = self.sources_dir,
        downloads_dir = self.downloads_dir,
        repo_url = self.repo_url,
        is_popout = false,
        is_borderless = true,
        title_bar_fm_style = true,
    })
end

function LibraryBrowser:onMenuSelect(item)
    if item.is_placeholder then
        return true
    end

    local entry = item.library_entry
    local MangaBrowser = require("mangabrowser")
    UIManager:show(MangaBrowser:new{
        engine = self.engine,
        store = self.store,
        downloads_engine = self.downloads_engine,
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
    if item.is_placeholder then
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
