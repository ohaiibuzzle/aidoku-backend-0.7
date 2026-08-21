--[[--
Flat list of every downloaded chapter, across all manga/sources. Tapping one
opens it in the reader; holding one removes both the file and its index
entry.
]]

local ConfirmBox = require("ui/widget/confirmbox")
local Menu = require("ui/widget/menu")
local UIManager = require("ui/uimanager")
local T = require("ffi/util").template
local _ = require("gettext")

local DownloadsBrowser = Menu:extend{
    title = _("Downloaded chapters"),
}

function DownloadsBrowser:init()
    self.item_table = self:genItemTable()
    Menu.init(self)
end

function DownloadsBrowser:genItemTable()
    local item_table = {}
    for _, entry in ipairs(self.store:allDownloads()) do
        table.insert(item_table, {
            text = entry.manga_title .. " - " .. entry.chapter_title,
            download_entry = entry,
        })
    end
    if #item_table == 0 then
        table.insert(item_table, { text = _("No downloaded chapters yet."), dim = true, is_placeholder = true })
    end
    return item_table
end

function DownloadsBrowser:refresh()
    self.item_table = self:genItemTable()
    self:updateItems()
end

function DownloadsBrowser:onMenuSelect(item)
    if item.is_placeholder then
        return true
    end
    local entry = item.download_entry
    if self.ui.document then
        self.ui:switchDocument(entry.path)
    else
        self.ui:openFile(entry.path)
    end
    return true
end

function DownloadsBrowser:onMenuHold(item)
    if item.is_placeholder then
        return true
    end
    local entry = item.download_entry
    UIManager:show(ConfirmBox:new{
        text = T(_("Remove downloaded chapter?\n\n%1"), item.text),
        ok_text = _("Remove"),
        ok_callback = function()
            self.store:removeDownload(entry.source_path, entry.manga_key, entry.chapter_key)
            self:refresh()
        end,
    })
    return true
end

return DownloadsBrowser
