--[[--
git : source management (installed sources, repository browser,
downloaded chapters), the chapter-prefetch and auto-advance preferences,
storage limit/usage, FlareSolverr host, and the source repository URL.
Reached from the Library screen's title bar, keeping the Library list
itself just the user's bookmarked manga.
]]

local ConfirmBox = require("ui/widget/confirmbox")
local InfoMessage = require("ui/widget/infomessage")
local InputDialog = require("ui/widget/inputdialog")
local Menu = require("ui/widget/menu")
local OpenWidgets = require("openwidgets")
local PathChooser = require("ui/widget/pathchooser")
local Store = require("store")
local Trapper = require("ui/trapper")
local UIManager = require("ui/uimanager")
local filemanagerutil = require("apps/filemanager/filemanagerutil")
local util = require("util")
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
    OpenWidgets.push(self)
    -- init() runs during SettingsBrowser:new{}, before the caller's own
    -- UIManager:show(self) -- see the same note in repobrowser.lua's
    -- init() for why the usage fetch must be deferred rather than done
    -- here.
    UIManager:nextTick(function() self:reloadUsage() end)
end

-- See librarybrowser.lua's onCloseWidget for why this is needed.
function SettingsBrowser:onCloseWidget()
    Menu.onCloseWidget(self)
    OpenWidgets.remove(self)
end

function SettingsBrowser:genItemTable()
    local storage_text = self.store:isEphemeralMode()
        and _("N/A")
        or (self.store:downloadLimitBytes() <= 0 and _("none") or formatMB(self.store:downloadLimitBytes()))
    if self.usage_bytes then
        storage_text = storage_text .. T(_(" (using %1)"), formatMB(self.usage_bytes))
    end

    local flaresolverr = self.store:flareSolverrHost()
    local flaresolverr_text = flaresolverr ~= "" and flaresolverr or _("not set")

    local repo_url_text = self.store:repoURL() == Store.DEFAULT_REPO_URL and _("Default") or _("Custom")

    local concurrency = self.store:networkConcurrency()
    local concurrency_text = concurrency > 0 and tostring(concurrency) or _("Default (8)")

    -- Section header rows are dim + is_placeholder (unselectable, see
    -- onMenuSelect) purely to visually group the settings below -- they
    -- carry no state of their own.
    return {
        { text = _("Sources"), mandatory=">", is_sources_entry = true },

        { text = _(""), dim = true, bold = true, is_placeholder = true },
        {
            text = _("Prefetch next chapters"),
            mandatory = self.store:isEphemeralMode()
                and tostring(self.store:effectiveBufferChapters())
                or tostring(self.store:bufferChapters()),
            is_buffer_entry = true,
        },
        {
            text = _("Auto-advance at chapter end"),
            mandatory = MODE_LABELS[self.store:nextChapterMode()],
            is_next_chapter_entry = true,
        },

        { text = _("Storage limit"), mandatory = storage_text, is_storage_limit_entry = true },

        -- Network/source configuration, roughly least to most obscure.
        { text = _("Source repository URL"), mandatory = repo_url_text, is_repo_url_entry = true },
        { text = _("Network concurrency"), mandatory = concurrency_text, is_network_concurrency_entry = true },
        
        -- Advanced settings that *most users* don't need to touch.
        { text = _(""), dim = true, bold = true, is_placeholder = true },
        {
            text = _("Ephemeral Mode"),
            mandatory = self.store:isEphemeralMode() and _("On") or _("Off"),
            is_ephemeral_mode_entry = true,
        },
        {
            text = _("Ephemeral storage path"),
            mandatory = self.store:ephemeralPath(),
            is_ephemeral_path_entry = true,
        },
        { text = _("FlareSolverr host"), mandatory = flaresolverr_text, is_flaresolverr_entry = true },
        { text = _("Import cookies"), is_import_cookies_entry = true },
        { text = _("Clear stored cookies"), is_clear_cookies_entry = true },
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
        description = _("Chapters to download ahead of time when you start reading one. 0 disables prefetching."),
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
        description = _("Oldest chapters are deleted first to stay under the limit. 0 means no limit."),
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
            _("The index.min.json URL the repository browser installs sources from. Leave blank to use the default:\n%1"),
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
        description = _("Number of requests a source can run at once via net.send_all. Leave blank or 0 to use the default (8). Higher values may use more memory."),
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

-- toggleEphemeralMode flips the setting directly, no dialog (same shape as
-- cycleNextChapterMode below). Turning it off purges whatever's left in the
-- RAM-disk dir first, while self.downloads_engine still points at it -- see
-- CLAUDE.md's Ephemeral Mode section.
--
-- self.aidoku:refreshDownloadsDir() (see main.lua) redirects downloads
-- immediately rather than only on this plugin instance's next init() --
-- without it, a download started right after toggling in this same
-- FileManager session would still land in the old location, since
-- self.downloads_dir/self.downloads_engine are plain fields copied once at
-- construction time everywhere they're threaded. self.refresh_callback
-- (threaded in from librarybrowser.lua) both resyncs LibraryBrowser's own
-- copies of those fields and updates the Library subtitle immediately.
function SettingsBrowser:importCookies()
    local chooser
    chooser = PathChooser:new{
        title = _("Long-press a cookies.txt to import it"),
        path = filemanagerutil.getHomeFolder(),
        select_directory = false,
        select_file = true,
        onConfirm = function(cookies_path)
            Trapper:wrap(function()
                local ok, err = self.engine:cookieLoad(cookies_path)
                if ok then
                    UIManager:show(InfoMessage:new{ text = ok, timeout = 3 })
                else
                    UIManager:show(InfoMessage:new{ text = T(_("Import failed:\n%1"), err) })
                end
            end)
        end,
    }
    UIManager:show(chooser)
end

function SettingsBrowser:confirmClearCookies()
    UIManager:show(ConfirmBox:new{
        text = _("Clear all cookies stored for every source? This can't be undone."),
        ok_text = _("Clear"),
        ok_callback = function()
            Trapper:wrap(function()
                local ok, err = self.engine:cookieClear()
                if ok then
                    UIManager:show(InfoMessage:new{ text = _("Cookies cleared."), timeout = 2 })
                else
                    UIManager:show(InfoMessage:new{ text = T(_("Could not clear cookies:\n%1"), err) })
                end
            end)
        end,
    })
end

function SettingsBrowser:toggleEphemeralMode()
    local enabling = not self.store:isEphemeralMode()
    if not enabling then
        self.downloads_engine:removeAllExcept(nil)
    end
    self.store:setEphemeralMode(enabling)
    self.aidoku:refreshDownloadsDir()
    self.downloads_dir = self.aidoku.downloads_dir
    self.downloads_engine = self.aidoku.downloads_engine
    self:refresh()
    if self.refresh_callback then
        self.refresh_callback()
    end
end

-- pathIsWritable checks a candidate Ephemeral Mode storage path by actually
-- creating it and writing a throwaway file, rather than trusting util.makePath
-- alone -- a path can exist as a directory but still not be writable (e.g. a
-- mount point that's read-only).
function SettingsBrowser:pathIsWritable(path)
    if not util.makePath(path) then
        return false
    end
    local test_file = path .. "/.aidoku_ephemeral_test"
    local f = io.open(test_file, "w")
    if not f then
        return false
    end
    f:close()
    os.remove(test_file)
    return true
end

function SettingsBrowser:promptEphemeralPath()
    local dialog
    dialog = InputDialog:new{
        title = _("Ephemeral storage path"),
        description = _("Directory to store downloaded chapters in while Ephemeral Mode is on."),
        input = self.store:ephemeralPath(),
        input_hint = "/dev/shm",
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
                    local path = dialog:getInputText()
                    UIManager:close(dialog)
                    path = path and path:gsub("^%s+", ""):gsub("%s+$", "") or ""
                    if path == "" then
                        return
                    end
                    if not self:pathIsWritable(path) then
                        UIManager:show(InfoMessage:new{
                            text = T(_("%1 doesn't exist or isn't writable."), path),
                        })
                        return
                    end
                    self.store:setEphemeralPath(path)
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
    if item.is_placeholder then
        return true
    end
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
        if self.store:isEphemeralMode() then
            UIManager:show(InfoMessage:new{
                text = _("You can only prefetch one chapter ahead while Ephemeral Mode is on."),
            })
        else
            self:promptBufferChapters()
        end
    elseif item.is_next_chapter_entry then
        self:cycleNextChapterMode()
    elseif item.is_storage_limit_entry then
        if self.store:isEphemeralMode() then
            UIManager:show(InfoMessage:new{
                text = _("Storage limit isn't applied while Ephemeral Mode is on."),
            })
        else
            self:promptStorageLimit()
        end
    elseif item.is_ephemeral_mode_entry then
        self:toggleEphemeralMode()
    elseif item.is_ephemeral_path_entry then
        self:promptEphemeralPath()
    elseif item.is_repo_url_entry then
        self:promptRepoURL()
    elseif item.is_network_concurrency_entry then
        self:promptNetworkConcurrency()
    elseif item.is_flaresolverr_entry then
        self:promptFlareSolverrHost()
    elseif item.is_import_cookies_entry then
        self:importCookies()
    elseif item.is_clear_cookies_entry then
        self:confirmClearCookies()
    end
    return true
end

return SettingsBrowser
