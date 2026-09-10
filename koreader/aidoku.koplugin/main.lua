--[[--
Aidoku plugin: browse Aidoku manga source repositories, search and download
chapters as CBZ via a bundled aidoku-run binary (see cmd/aidoku-run in the
parent Go repo), and read them in KOReader.
]]

local DataStorage = require("datastorage")
local Dispatcher = require("dispatcher") -- luacheck:ignore
local InfoMessage = require("ui/widget/infomessage")
local UIManager = require("ui/uimanager")
local WidgetContainer = require("ui/widget/container/widgetcontainer")
local util = require("util")
local _ = require("gettext")

local ChapterCache = require("chaptercache")
local DownloadsEngine = require("downloadsengine")
local Engine = require("engine")
local OpenWidgets = require("openwidgets")
local Store = require("store")

local Aidoku = WidgetContainer:extend{
    name = "aidoku",
    is_doc_only = false,
}

-- binArch returns the plugin's bin/ subdirectory for the running device.
-- jit.arch (LuaJIT's arch name) matches Go's GOARCH for arm64 and x86-64
-- ("x64"), but unlike GOARCH it doesn't distinguish ARM versions -- it
-- reports "arm" on both armv6 and armv7 hardware. koreader/build.sh ships
-- separate armv6/armv7 binaries (older Kindles -- Kindle 4, Touch, PW1 --
-- are ARMv6; newer Kindles and Kobos are ARMv7), so on "arm" we read
-- /proc/cpuinfo's "CPU architecture" line to pick between them, falling
-- back to the more common armv7 if that's ever unavailable/unparseable.
local function binArch()
    if jit.arch ~= "arm" then
        return jit.arch
    end
    local cpuinfo = io.open("/proc/cpuinfo", "r")
    if cpuinfo then
        for line in cpuinfo:lines() do
            local ver = line:match("^CPU architecture%s*:%s*(%d+)")
            if ver then
                cpuinfo:close()
                return tonumber(ver) <= 6 and "armv6" or "armv7"
            end
        end
        cpuinfo:close()
    end
    return "armv7"
end

-- dataSubdir builds a path under DataStorage's data dir, stripped of a
-- leading "./" -- DataStorage:getDataDir() can return the bare "." on
-- devices that exec a relative "./reader.lua" (confirmed on a real
-- Kindle), and KOReader's own recorded document paths never carry "./",
-- so leaving it in self.downloads_dir silently breaks every
-- `file:sub(1, #self.downloads_dir) == self.downloads_dir` prefix check
-- below and in nextchapter.lua (read-marking, auto-advance, Library
-- return all no-op with no error). Stripping it here is a no-op for I/O
-- itself.
local function dataSubdir(name)
    return (DataStorage:getDataDir() .. "/aidoku/" .. name):gsub("^%./", "")
end

function Aidoku:init()
    self.sources_dir = dataSubdir("sources")
    self.settings_dir = dataSubdir("settings")
    util.makePath(self.sources_dir)
    util.makePath(self.settings_dir)

    -- self.path is set by KOReader's plugin loader to this plugin's own
    -- directory; bin_arch (see binArch() above) names match the targets
    -- koreader/build.sh produces ("armv6"/"armv7" for Kindles/Kobos,
    -- "arm64"/"x64" for aarch64/x86-64 devices and desktops).
    local bin_arch = binArch()
    self.store = Store.new(dataSubdir(""))
    self.engine = Engine.new(self.path .. "/bin/" .. bin_arch .. "/aidoku-run", function()
        local concurrency = self.store:networkConcurrency()
        return {
            FLARESOLVERR_HOST = self.store:flareSolverrHost(),
            AIDOKU_SETTINGS_DIR = self.settings_dir,
            NETWORK_CONCURRENCY = concurrency > 0 and tostring(concurrency) or "",
        }
    end)

    -- Must run after self.store exists -- see refreshDownloadsDir()'s own doc
    -- comment for why this isn't just done once here.
    self:refreshDownloadsDir()

    self:onDispatcherRegisterActions()
    self.ui.menu:registerToMainMenu(self)
    self:hookEndOfBook()
    self:hookShowFileManager()
    self:hookClose()
    self:openPendingLibrary()
end

-- refreshDownloadsDir (re)computes self.downloads_dir/self.downloads_engine
-- to either the normal data dir or Ephemeral Mode's RAM-disk path,
-- depending on the current setting. Called from init() and again from
-- settingsbrowser.lua's toggleEphemeralMode() so the toggle redirects
-- downloads immediately rather than only on the next plugin-instance
-- construction.
function Aidoku:refreshDownloadsDir()
    self.downloads_dir = self.store:isEphemeralMode()
        and self.store:ephemeralPath() or dataSubdir("downloads")
    util.makePath(self.downloads_dir)
    self.downloads_engine = DownloadsEngine.new(self.downloads_dir)
end

-- markCurrentChapterRead flags self.document's chapter as read (see
-- store.lua's read_chapters), independent of next_chapter_mode -- unlike
-- nextchapter.lua's own byPath lookup, this must run even when
-- auto-advance is off, so it's a separate lookup rather than something
-- threaded through NextChapter.handle(). downloads_engine:byPath() is a
-- synchronous SQLite read (see downloadsengine.lua), so no Trapper:wrap()
-- coroutine is needed here.
function Aidoku:markCurrentChapterRead()
    local file = self.document and self.document.file
    if not file or file:sub(1, #self.downloads_dir) ~= self.downloads_dir then
        return
    end
    local entry = self.downloads_engine:byPath(file)
    if entry then
        self.store:markChapterRead(entry.sourceKey, entry.mangaKey, entry.chapterKey)
    end
end

-- hookEndOfBook wires up auto-advance to the next chapter. Only applies
-- inside ReaderUI (self.document is only set there, not FileManager).
--
-- Can't suppress ReaderStatus's own "end of document" pop-up via a
-- same-named event handler here -- it's registered before plugins and
-- never returns true, so propagation always reaches it regardless. Instead
-- this monkey-patches its onEndOfBook method directly: ours runs first and
-- falls through to the original when the document isn't one of ours.
function Aidoku:hookEndOfBook()
    if not self.document or not self.ui.status then
        return
    end
    local original_on_end_of_book = self.ui.status.onEndOfBook
    self.ui.status.onEndOfBook = function(status_self, ...)
        self:markCurrentChapterRead()
        local NextChapter = require("nextchapter")
        local handled = NextChapter.handle({
            engine = self.engine,
            store = self.store,
            downloads_engine = self.downloads_engine,
            downloads_dir = self.downloads_dir,
            sources_dir = self.sources_dir,
            open_callback = function(path) self.ui:switchDocument(path) end,
        }, self.document.file)
        if handled then
            return true
        end
        return original_on_end_of_book(status_self, ...)
    end
end

-- hookShowFileManager makes every "back to Files" path return to Aidoku's
-- own Library screen instead of KOReader's raw file browser, when the
-- chapter being closed is one of ours. Every such path (onHome, the
-- end-of-document pop-up, the top-bar file-browser icon) funnels through
-- ReaderUI:showFileManager(file), so hooking that one method (same
-- monkey-patch technique as hookEndOfBook) covers all of them.
--
-- showFileManager tears down this plugin instance and spins up a new
-- FileManager (and, since is_doc_only = false, a new Aidoku instance for
-- it) -- with no direct way to hand that future instance a "reopen the
-- Library" instruction, this leaves a note in pendinglibrary.lua (a
-- require()-cached table, shared across the teardown/recreate boundary)
-- for its init() to act on -- see openPendingLibrary() below.
function Aidoku:hookShowFileManager()
    if not self.document then
        return
    end
    local original_show_file_manager = self.ui.showFileManager
    self.ui.showFileManager = function(ui_self, file, ...)
        file = file or (self.document and self.document.file)
        if file and file:sub(1, #self.downloads_dir) == self.downloads_dir then
            require("pendinglibrary").open = true
        end
        return original_show_file_manager(ui_self, file, ...)
    end
end

-- hookClose closes any Aidoku screen still open (openwidgets.lua) right
-- before self.ui closes -- covers Exit, Restart, and the Reader<->
-- FileManager handoff, all of which route through onClose(). Each Aidoku
-- screen is its own top-level UIManager widget, not a child of self.ui, so
-- drilling from one into another leaves ancestors buried on the window
-- stack; UIManager's run loop only stops once that stack is fully empty,
-- so a leftover screen silently blocks KOReader from quitting (see
-- openwidgets.lua). Applies to both FileManager and ReaderUI instances,
-- unlike the ReaderUI-only hooks above.
function Aidoku:hookClose()
    local original_on_close = self.ui.onClose
    self.ui.onClose = function(ui_self, ...)
        OpenWidgets.closeAll()
        return original_on_close(ui_self, ...)
    end
end

-- Reopens the Library screen on top of a freshly shown FileManager when a
-- previous ReaderUI instance's hookShowFileManager() (above) requested it.
function Aidoku:openPendingLibrary()
    if self.document then
        return
    end
    local pending = require("pendinglibrary")
    if not pending.open then
        return
    end
    pending.open = false
    UIManager:nextTick(function() self:onAidokuBrowseSources() end)
end

function Aidoku:onDispatcherRegisterActions()
    Dispatcher:registerAction("aidoku_browse_sources", {
        category = "none",
        event = "AidokuBrowseSources",
        title = _("Aidoku"),
        filemanager = true,
    })
end

function Aidoku:addToMainMenu(menu_items)
    if not self.ui.document then -- FileManager menu only
        menu_items.aidoku = {
            text = _("AidokuRunner for KOReader"),
            sorting_hint = "search",
            callback = function()
                self:onAidokuBrowseSources()
            end,
        }
    end
end

-- Every entry into Library -- a fresh open from the FileManager menu, or
-- the Reader->FileManager handoff reopening it via openPendingLibrary()
-- above -- wipes chaptercache.lua, so a manga's chapter list refreshes for
-- real next time it's opened rather than staying pinned to whatever was
-- cached from an earlier read session. See chaptercache.lua's own note for
-- why it stays valid across chapter-to-chapter advances within one session.
function Aidoku:onAidokuBrowseSources()
    ChapterCache.clear()
    if not self.engine:isAvailable() then
        UIManager:show(InfoMessage:new{
            text = _("The bundled aidoku-run binary was not found. Run koreader/build.sh in the aidokurunner-go repo to build and bundle it before using this plugin."),
        })
        return
    end
    if not self.downloads_engine:isAvailable() then
        UIManager:show(InfoMessage:new{
            text = _("Could not open the downloads database. Check that the storage device is writable."),
        })
        return
    end

    local LibraryBrowser = require("librarybrowser")
    UIManager:show(LibraryBrowser:new{
        aidoku = self,
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
end

return Aidoku
