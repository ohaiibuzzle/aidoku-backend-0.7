--[[--
Flat list of every downloaded chapter, across all manga/sources. Tapping one
opens it in the reader; holding one removes both the file and its index
entry.
]]

local ConfirmBox = require("ui/widget/confirmbox")
local InfoMessage = require("ui/widget/infomessage")
local Menu = require("ui/widget/menu")
local OpenWidgets = require("openwidgets")
local Trapper = require("ui/trapper")
local UIManager = require("ui/uimanager")
local lfs = require("libs/libkoreader-lfs")
local T = require("ffi/util").template
local _ = require("gettext")

local DownloadsBrowser = Menu:extend{
    title = _("Downloaded chapters"),
}

function DownloadsBrowser:init()
    self.item_table = {}
    Menu.init(self)
    OpenWidgets.push(self)
    -- init() runs during DownloadsBrowser:new{}, before the caller's own
    -- UIManager:show(self) -- see the same note in repobrowser.lua's
    -- init() for why this must be deferred rather than loaded here.
    UIManager:nextTick(function() self:reload() end)
end

-- See librarybrowser.lua's onCloseWidget for why this is needed.
function DownloadsBrowser:onCloseWidget()
    Menu.onCloseWidget(self)
    OpenWidgets.remove(self)
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

-- See mangabrowser.lua's downloadedPath()/onMenuSelect() for the same
-- pattern -- this list is built from a list() snapshot that can go stale
-- (a row whose file was since deleted, e.g. by prune() or Ephemeral Mode's
-- residency bound), so re-check existence rather than trusting it blindly,
-- and apply the same markLastRead/removeAllExcept bookkeeping every other
-- "open a downloaded chapter" path already does.
function DownloadsBrowser:onMenuSelect(item)
    if item.is_placeholder then
        return true
    end
    local entry = item.download_entry
    if lfs.attributes(entry.path, "mode") ~= "file" then
        UIManager:show(InfoMessage:new{ text = _("That download is no longer available."), timeout = 2 })
        self:reload()
        return true
    end
    self.store:markLastRead(entry.sourceKey, entry.mangaKey)
    if self.ui.document then
        self.ui:switchDocument(entry.path)
    else
        self.ui:openFile(entry.path)
    end
    if self.store:isEphemeralMode() then
        self.downloads_engine:removeAllExcept(entry.path)
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
