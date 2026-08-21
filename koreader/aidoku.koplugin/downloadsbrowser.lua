--[[--
Flat list of every downloaded chapter, across all manga/sources. Tapping one
opens it in the reader; holding one removes both the file and its index
entry.
]]

local ConfirmBox = require("ui/widget/confirmbox")
local InfoMessage = require("ui/widget/infomessage")
local Menu = require("ui/widget/menu")
local Trapper = require("ui/trapper")
local UIManager = require("ui/uimanager")
local T = require("ffi/util").template
local _ = require("gettext")

local DownloadsBrowser = Menu:extend{
    title = _("Downloaded chapters"),
}

function DownloadsBrowser:init()
    self.item_table = {}
    Menu.init(self)
    -- init() runs during DownloadsBrowser:new{}, before the caller's own
    -- UIManager:show(self) -- see the same note in repobrowser.lua's
    -- init() for why this must be deferred rather than loaded here.
    UIManager:nextTick(function() self:reload() end)
end

function DownloadsBrowser:genItemTable(entries)
    local item_table = {}
    for _, entry in ipairs(entries) do
        table.insert(item_table, {
            text = entry.mangaTitle .. " - " .. entry.chapterTitle,
            download_entry = entry,
        })
    end
    if #item_table == 0 then
        table.insert(item_table, { text = _("No downloaded chapters yet."), dim = true, is_placeholder = true })
    end
    return item_table
end

-- loadAndRefresh assumes it's already running inside a Trapper-wrapped
-- coroutine (reload() provides that for the initial load; onMenuHold's
-- remove-then-refresh already has its own wrap and calls this directly,
-- rather than nesting a second one via reload()).
function DownloadsBrowser:loadAndRefresh()
    local entries, err = self.downloads_engine:list()
    if not entries then
        UIManager:show(InfoMessage:new{ text = T(_("Could not load downloads:\n%1"), err) })
        return
    end
    self.item_table = self:genItemTable(entries)
    self:updateItems()
end

function DownloadsBrowser:reload()
    Trapper:wrap(function() self:loadAndRefresh() end)
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
            Trapper:wrap(function()
                self.downloads_engine:remove(entry.sourceKey, entry.mangaKey, entry.chapterKey)
                self:loadAndRefresh()
            end)
        end,
    })
    return true
end

return DownloadsBrowser
