--[[--
Top-level screen: lists locally installed .aix sources, plus an entry to
browse the source repository and install more. Tapping an installed source
opens SearchBrowser against it.
]]

local ConfirmBox = require("ui/widget/confirmbox")
local Menu = require("ui/widget/menu")
local UIManager = require("ui/uimanager")
local lfs = require("libs/libkoreader-lfs")
local T = require("ffi/util").template
local _ = require("gettext")

local SourcesBrowser = Menu:extend{
    title = _("Aidoku sources"),
}

local BROWSE_REPO_TEXT = _("Browse source repository…")
local DOWNLOADS_TEXT = _("Downloaded chapters")

function SourcesBrowser:init()
    self.item_table = self:genItemTable()
    Menu.init(self)
end

function SourcesBrowser:genItemTable()
    local item_table = {}
    table.insert(item_table, { text = BROWSE_REPO_TEXT, is_repo_entry = true })
    table.insert(item_table, { text = DOWNLOADS_TEXT, is_downloads_entry = true })
    for entry in lfs.dir(self.sources_dir) do
        if entry:match("%.aix$") then
            table.insert(item_table, {
                text = entry:gsub("%.aix$", ""),
                path = self.sources_dir .. "/" .. entry,
            })
        end
    end
    return item_table
end

function SourcesBrowser:refresh()
    self.item_table = self:genItemTable()
    self:updateItems()
end

function SourcesBrowser:onMenuSelect(item)
    if item.is_repo_entry then
        local RepoBrowser = require("repobrowser")
        UIManager:show(RepoBrowser:new{
            engine = self.engine,
            repo_url = self.repo_url,
            sources_dir = self.sources_dir,
            is_popout = false,
            is_borderless = true,
            title_bar_fm_style = true,
            refresh_callback = function() self:refresh() end,
        })
    elseif item.is_downloads_entry then
        local DownloadsBrowser = require("downloadsbrowser")
        UIManager:show(DownloadsBrowser:new{
            store = self.store,
            ui = self.ui,
            is_popout = false,
            is_borderless = true,
            title_bar_fm_style = true,
        })
    else
        local SearchBrowser = require("searchbrowser")
        UIManager:show(SearchBrowser:new{
            engine = self.engine,
            store = self.store,
            ui = self.ui,
            source_path = item.path,
            downloads_dir = self.downloads_dir,
            title = item.text,
            is_popout = false,
            is_borderless = true,
            title_bar_fm_style = true,
        })
    end
    return true
end

function SourcesBrowser:onMenuHold(item)
    if item.is_repo_entry or item.is_downloads_entry then
        return true
    end
    UIManager:show(ConfirmBox:new{
        text = T(_("Remove installed source '%1'?"), item.text),
        ok_text = _("Remove"),
        ok_callback = function()
            os.remove(item.path)
            self:refresh()
        end,
    })
    return true
end

return SourcesBrowser
