--[[--
Top-level screen: lists locally installed .aix sources plus an entry to
view downloaded chapters. Tapping an installed source opens SearchBrowser
against it. Browsing the source repository is the title bar's "+" button;
searching every installed source at once is Library's "Global Search"
entry (see librarybrowser.lua) rather than living here.
]]

local ButtonDialog = require("ui/widget/buttondialog")
local ConfirmBox = require("ui/widget/confirmbox")
local InstalledSources = require("installedsources")
local Menu = require("ui/widget/menu")
local OpenWidgets = require("openwidgets")
local Trapper = require("ui/trapper")
local UIManager = require("ui/uimanager")
local T = require("ffi/util").template
local _ = require("gettext")

local SourcesBrowser = Menu:extend{
    title = _("Aidoku sources"),
    title_bar_left_icon = "plus",
}

local DOWNLOADS_TEXT = _("Downloaded chapters")

function SourcesBrowser:init()
    self.item_table = {}
    Menu.init(self)
    OpenWidgets.push(self)
    -- init() runs during SourcesBrowser:new{}, before the caller's own
    -- UIManager:show(self) -- see the same note in repobrowser.lua's
    -- init() for why this must be deferred rather than loaded here.
    -- Needed now that InstalledSources.list() reads each installed
    -- source's manifest via a subprocess call (see installedsources.lua)
    -- instead of just listing files.
    UIManager:nextTick(function() self:reload() end)
end

-- See librarybrowser.lua's onCloseWidget for why this is needed.
function SourcesBrowser:onCloseWidget()
    Menu.onCloseWidget(self)
    OpenWidgets.remove(self)
end

function SourcesBrowser:genItemTable()
    local item_table = {}
    table.insert(item_table, { text = DOWNLOADS_TEXT, is_downloads_entry = true })
    for _, source in ipairs(InstalledSources.list(self.sources_dir, self.engine)) do
        table.insert(item_table, source)
    end
    return item_table
end

-- The "+" title bar button (see title_bar_left_icon above) is this
-- screen's entry to the repository browser, replacing what used to be a
-- "Browse source repository…" row in the list itself. Reads the URL fresh
-- from the store (rather than a repo_url field threaded down from
-- main.lua) so a change made in settingsbrowser.lua's "Source repository
-- URL" setting takes effect immediately, even if this screen was already
-- open when it changed.
function SourcesBrowser:onLeftButtonTap()
    local RepoBrowser = require("repobrowser")
    UIManager:show(RepoBrowser:new{
        engine = self.engine,
        repo_url = self.store:repoURL(),
        sources_dir = self.sources_dir,
        is_popout = false,
        is_borderless = true,
        title_bar_fm_style = true,
        refresh_callback = function() self:reload() end,
    })
end

-- loadAndRefresh assumes it's already running inside a Trapper-wrapped
-- coroutine (reload() provides that for the initial load and for
-- refresh_callback/remove below; avoid calling this directly from
-- somewhere unwrapped).
function SourcesBrowser:loadAndRefresh()
    self.item_table = self:genItemTable()
    self:updateItems()
end

function SourcesBrowser:reload()
    Trapper:wrap(function() self:loadAndRefresh() end)
end

function SourcesBrowser:onMenuSelect(item)
    if item.is_downloads_entry then
        local DownloadsBrowser = require("downloadsbrowser")
        UIManager:show(DownloadsBrowser:new{
            downloads_engine = self.downloads_engine,
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
            downloads_engine = self.downloads_engine,
            ui = self.ui,
            source_path = item.path,
            source_key = item.key,
            source_name = item.name,
            downloads_dir = self.downloads_dir,
            title = item.text,
            is_popout = false,
            is_borderless = true,
            title_bar_fm_style = true,
            refresh_callback = self.refresh_callback,
        })
    end
    return true
end

function SourcesBrowser:onMenuHold(item)
    if item.is_downloads_entry then
        return true
    end
    local dialog
    dialog = ButtonDialog:new{
        title = item.text,
        buttons = {
            {{
                text = _("Source settings"),
                callback = function()
                    UIManager:close(dialog)
                    local SourceSettingsBrowser = require("sourcesettingsbrowser")
                    UIManager:show(SourceSettingsBrowser:new{
                        engine = self.engine,
                        source_path = item.path,
                        source_text = item.text,
                        is_popout = false,
                        is_borderless = true,
                        title_bar_fm_style = true,
                    })
                end,
            }},
            {{
                text = _("Remove installed source"),
                callback = function()
                    UIManager:close(dialog)
                    UIManager:show(ConfirmBox:new{
                        text = T(_("Remove installed source '%1'?"), item.text),
                        ok_text = _("Remove"),
                        ok_callback = function()
                            os.remove(item.path)
                            self:reload()
                        end,
                    })
                end,
            }},
        },
    }
    UIManager:show(dialog)
    return true
end

return SourcesBrowser
