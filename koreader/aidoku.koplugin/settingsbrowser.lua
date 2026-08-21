--[[--
Aidoku settings: source management (installed sources, repository browser,
downloaded chapters), the chapter-prefetch and auto-advance preferences,
storage limit/usage, and FlareSolverr host. Reached from the Library
screen's title bar, keeping the Library list itself just the user's
bookmarked manga.
]]

local InputDialog = require("ui/widget/inputdialog")
local Menu = require("ui/widget/menu")
local Trapper = require("ui/trapper")
local UIManager = require("ui/uimanager")
local T = require("ffi/util").template
local _ = require("gettext")

local SettingsBrowser = Menu:extend{
    title = _("Aidoku settings"),
}

local MB = 1024 * 1024

-- next_chapter_mode cycles through these in order each time its row is
-- tapped -- see the note on this in mangabrowser.lua/main.lua about the
-- end-of-book auto-advance hook this setting is for, which still needs its
-- own reader-API research pass before it reads this value.
local MODE_ORDER = { "off", "ask", "auto" }
local MODE_LABELS = { off = _("Off"), ask = _("Ask"), auto = _("Auto") }

local function formatMB(bytes)
    return string.format("%.1f MB", bytes / MB)
end

function SettingsBrowser:init()
    self.usage_bytes = nil -- fetched async, see reloadUsage()
    self.item_table = self:genItemTable()
    Menu.init(self)
    -- init() runs during SettingsBrowser:new{}, before the caller's own
    -- UIManager:show(self) -- see the same note in repobrowser.lua's
    -- init() for why the usage fetch must be deferred rather than done
    -- here.
    UIManager:nextTick(function() self:reloadUsage() end)
end

function SettingsBrowser:genItemTable()
    local limit = self.store:downloadLimitBytes()
    local storage_text
    if limit <= 0 then
        storage_text = _("Storage limit: none")
    else
        storage_text = T(_("Storage limit: %1"), formatMB(limit))
    end
    if self.usage_bytes then
        storage_text = storage_text .. T(_(" (using %1)"), formatMB(self.usage_bytes))
    end

    local flaresolverr = self.store:flareSolverrHost()
    local flaresolverr_text = flaresolverr ~= "" and flaresolverr or _("not set")

    return {
        { text = _("Sources"), is_sources_entry = true },
        { text = T(_("Prefetch next chapters: %1"), self.store:bufferChapters()), is_buffer_entry = true },
        {
            text = T(_("Auto-advance at chapter end: %1"), MODE_LABELS[self.store:nextChapterMode()]),
            is_next_chapter_entry = true,
        },
        { text = storage_text, is_storage_limit_entry = true },
        { text = T(_("FlareSolverr host: %1"), flaresolverr_text), is_flaresolverr_entry = true },
    }
end

function SettingsBrowser:refresh()
    self.item_table = self:genItemTable()
    self:updateItems()
end

function SettingsBrowser:reloadUsage()
    Trapper:wrap(function()
        self.usage_bytes = self.downloads_engine:totalBytes()
        self:refresh()
    end)
end

function SettingsBrowser:promptBufferChapters()
    local dialog
    dialog = InputDialog:new{
        title = _("Prefetch next chapters"),
        description = _("How many upcoming chapters to download ahead of time when you start reading one. 0 disables prefetching."),
        input = tostring(self.store:bufferChapters()),
        input_type = "number",
        buttons = {{
            {
                text = _("Cancel"),
                id = "close",
                callback = function() UIManager:close(dialog) end,
            },
            {
                text = _("Set"),
                is_enter_default = true,
                callback = function()
                    local n = tonumber(dialog:getInputText())
                    UIManager:close(dialog)
                    if n and n >= 0 then
                        self.store:setBufferChapters(math.floor(n))
                        self:refresh()
                    end
                end,
            },
        }},
    }
    UIManager:show(dialog)
    dialog:onShowKeyboard()
end

function SettingsBrowser:promptStorageLimit()
    local current = self.store:downloadLimitBytes()
    local dialog
    dialog = InputDialog:new{
        title = _("Storage limit (MB)"),
        description = _("Once downloads exceed this, the oldest chapters are deleted first to stay under it. 0 means no limit."),
        input = current > 0 and tostring(current / MB) or "0",
        input_type = "number",
        buttons = {{
            {
                text = _("Cancel"),
                id = "close",
                callback = function() UIManager:close(dialog) end,
            },
            {
                text = _("Set"),
                is_enter_default = true,
                callback = function()
                    local mb = tonumber(dialog:getInputText())
                    UIManager:close(dialog)
                    if mb and mb >= 0 then
                        self.store:setDownloadLimitBytes(math.floor(mb * MB))
                        self:refresh()
                    end
                end,
            },
        }},
    }
    UIManager:show(dialog)
    dialog:onShowKeyboard()
end

function SettingsBrowser:promptFlareSolverrHost()
    local dialog
    dialog = InputDialog:new{
        title = _("FlareSolverr host"),
        description = _("e.g. localhost:8191 -- used to solve Cloudflare challenges for sources that need it. Leave blank to disable."),
        input = self.store:flareSolverrHost(),
        input_hint = _("host:port"),
        buttons = {{
            {
                text = _("Cancel"),
                id = "close",
                callback = function() UIManager:close(dialog) end,
            },
            {
                text = _("Set"),
                is_enter_default = true,
                callback = function()
                    local host = dialog:getInputText()
                    UIManager:close(dialog)
                    self.store:setFlareSolverrHost(host and host:gsub("^%s+", ""):gsub("%s+$", "") or "")
                    self:refresh()
                end,
            },
        }},
    }
    UIManager:show(dialog)
    dialog:onShowKeyboard()
end

function SettingsBrowser:cycleNextChapterMode()
    local current = self.store:nextChapterMode()
    local next_mode = MODE_ORDER[1]
    for i, mode in ipairs(MODE_ORDER) do
        if mode == current then
            next_mode = MODE_ORDER[(i % #MODE_ORDER) + 1]
            break
        end
    end
    self.store:setNextChapterMode(next_mode)
    self:refresh()
end

function SettingsBrowser:onMenuSelect(item)
    if item.is_sources_entry then
        local SourcesBrowser = require("sourcesbrowser")
        UIManager:show(SourcesBrowser:new{
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
    elseif item.is_buffer_entry then
        self:promptBufferChapters()
    elseif item.is_next_chapter_entry then
        self:cycleNextChapterMode()
    elseif item.is_storage_limit_entry then
        self:promptStorageLimit()
    elseif item.is_flaresolverr_entry then
        self:promptFlareSolverrHost()
    end
    return true
end

return SettingsBrowser
