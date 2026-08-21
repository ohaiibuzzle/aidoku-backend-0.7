--[[--
Top-level screen: lists locally installed .aix sources, plus entries to
browse the source repository, search every installed source at once, and
view downloaded chapters. Tapping an installed source opens SearchBrowser
against it.
]]

local ButtonDialog = require("ui/widget/buttondialog")
local ConfirmBox = require("ui/widget/confirmbox")
local InstalledSources = require("installedsources")
local Menu = require("ui/widget/menu")
local Trapper = require("ui/trapper")
local UIManager = require("ui/uimanager")
local T = require("ffi/util").template
local _ = require("gettext")

local SourcesBrowser = Menu:extend{
    title = _("Aidoku sources"),
}

local BROWSE_REPO_TEXT = _("Browse source repository…")
local SEARCH_ALL_TEXT = _("Search all sources…")
local DOWNLOADS_TEXT = _("Downloaded chapters")

function SourcesBrowser:init()
    self.item_table = {}
    Menu.init(self)
    -- init() runs during SourcesBrowser:new{}, before the caller's own
    -- UIManager:show(self) -- see the same note in repobrowser.lua's
    -- init() for why this must be deferred rather than loaded here.
    -- Needed now that InstalledSources.list() reads each installed
    -- source's manifest via a subprocess call (see installedsources.lua)
    -- instead of just listing files.
    UIManager:nextTick(function() self:reload() end)
end

function SourcesBrowser:genItemTable()
    local item_table = {}
    table.insert(item_table, { text = BROWSE_REPO_TEXT, is_repo_entry = true })
    table.insert(item_table, { text = SEARCH_ALL_TEXT, is_search_all_entry = true })
    table.insert(item_table, { text = DOWNLOADS_TEXT, is_downloads_entry = true })
    for _, source in ipairs(InstalledSources.list(self.sources_dir, self.engine)) do
        table.insert(item_table, source)
    end
    return item_table
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
    if item.is_repo_entry then
        local RepoBrowser = require("repobrowser")
        UIManager:show(RepoBrowser:new{
            engine = self.engine,
            repo_url = self.repo_url,
            sources_dir = self.sources_dir,
            is_popout = false,
            is_borderless = true,
            title_bar_fm_style = true,
            refresh_callback = function() self:reload() end,
        })
    elseif item.is_search_all_entry then
        local GlobalSearchBrowser = require("globalsearchbrowser")
        UIManager:show(GlobalSearchBrowser:new{
            engine = self.engine,
            store = self.store,
            downloads_engine = self.downloads_engine,
            ui = self.ui,
            sources_dir = self.sources_dir,
            downloads_dir = self.downloads_dir,
            is_popout = false,
            is_borderless = true,
            title_bar_fm_style = true,
        })
    elseif item.is_downloads_entry then
        local DownloadsBrowser = require("downloadsbrowser")
        UIManager:show(DownloadsBrowser:new{
            downloads_engine = self.downloads_engine,
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
    if item.is_repo_entry or item.is_search_all_entry or item.is_downloads_entry then
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
