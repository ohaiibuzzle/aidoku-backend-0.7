--[[--
git : source management (installed sources, repository browser,
downloaded chapters), the chapter-prefetch and auto-advance preferences,
storage limit/usage, FlareSolverr host, and the source repository URL.
Reached from the Library screen's title bar, keeping the Library list
itself just the user's bookmarked manga.
]]

local InputDialog = require("ui/widget/inputdialog")
local Menu = require("ui/widget/menu")
local Store = require("store")
local Trapper = require("ui/trapper")
local UIManager = require("ui/uimanager")
local T = require("ffi/util").template
local _ = require("gettext")

local SettingsBrowser = Menu:extend{
    title = _("Settings"),
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
    local storage_text = limit <= 0 and _("none") or formatMB(limit)
    if self.usage_bytes then
        storage_text = storage_text .. T(_(" (using %1)"), formatMB(self.usage_bytes))
    end

    local flaresolverr = self.store:flareSolverrHost()
    local flaresolverr_text = flaresolverr ~= "" and flaresolverr or _("not set")

    local repo_url_text = self.store:repoURL() == Store.DEFAULT_REPO_URL and _("Default") or _("Custom")

    local concurrency = self.store:networkConcurrency()
    local concurrency_text = concurrency > 0 and tostring(concurrency) or _("Default (8)")

    return {
        -- Navigation entry to the source-management screen.
        { text = _("Sources"), is_sources_entry = true },

        -- Day-to-day reading behavior.
        {
            text = _("Prefetch next chapters"),
            mandatory = tostring(self.store:bufferChapters()),
            is_buffer_entry = true,
        },
        {
            text = _("Auto-advance at chapter end"),
            mandatory = MODE_LABELS[self.store:nextChapterMode()],
            is_next_chapter_entry = true,
        },

        -- Storage.
        { text = _("Storage limit"), mandatory = storage_text, is_storage_limit_entry = true },

        -- Network/source configuration, roughly least to most obscure.
        { text = _("Source repository URL"), mandatory = repo_url_text, is_repo_url_entry = true },
        { text = _("Network concurrency"), mandatory = concurrency_text, is_network_concurrency_entry = true },
        { text = _("FlareSolverr host"), mandatory = flaresolverr_text, is_flaresolverr_entry = true },
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

function SettingsBrowser:promptRepoURL()
    local dialog
    dialog = InputDialog:new{
        title = _("Source repository URL"),
        description = T(
            _("The index.min.json URL the repository browser installs sources from. Leave blank to reset to the default:\n%1"),
            Store.DEFAULT_REPO_URL),
        input = self.store:repoURL(),
        input_hint = _("https://…/index.min.json"),
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
                    local url = dialog:getInputText()
                    UIManager:close(dialog)
                    self.store:setRepoURL(url or "")
                    self:refresh()
                end,
            },
        }},
    }
    UIManager:show(dialog)
    dialog:onShowKeyboard()
end

function SettingsBrowser:promptNetworkConcurrency()
    local current = self.store:networkConcurrency()
    local dialog
    dialog = InputDialog:new{
        title = _("Network concurrency"),
        description = _("How many requests a source can run at once via net.send_all. Leave blank or 0 to use the default (8). Higher values may use more memory."),
        input = current > 0 and tostring(current) or "",
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
                    local text = dialog:getInputText()
                    local n = tonumber(text)
                    UIManager:close(dialog)
                    if text == "" then
                        self.store:setNetworkConcurrency(0)
                        self:refresh()
                    elseif n and n > 0 then
                        self.store:setNetworkConcurrency(math.floor(n))
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
            is_popout = false,
            is_borderless = true,
            title_bar_fm_style = true,
            refresh_callback = self.refresh_callback,
        })
    elseif item.is_buffer_entry then
        self:promptBufferChapters()
    elseif item.is_next_chapter_entry then
        self:cycleNextChapterMode()
    elseif item.is_storage_limit_entry then
        self:promptStorageLimit()
    elseif item.is_repo_url_entry then
        self:promptRepoURL()
    elseif item.is_network_concurrency_entry then
        self:promptNetworkConcurrency()
    elseif item.is_flaresolverr_entry then
        self:promptFlareSolverrHost()
    end
    return true
end

return SettingsBrowser
