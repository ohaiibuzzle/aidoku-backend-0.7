--[[--
Aidoku plugin: browse Aidoku manga source repositories, search and download
chapters as CBZ via a bundled aidoku-run binary (see cmd/aidoku-run in the
parent Go repo), and read them in KOReader.
]]

local DataStorage = require("datastorage")
local Dispatcher = require("dispatcher") -- luacheck:ignore
local InfoMessage = require("ui/widget/infomessage")
local Trapper = require("ui/trapper")
local UIManager = require("ui/uimanager")
local WidgetContainer = require("ui/widget/container/widgetcontainer")
local util = require("util")
local _ = require("gettext")

local DownloadsEngine = require("downloadsengine")
local Engine = require("engine")
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

function Aidoku:init()
    self.sources_dir = DataStorage:getDataDir() .. "/aidoku/sources"
    self.downloads_dir = DataStorage:getDataDir() .. "/aidoku/downloads"
    self.settings_dir = DataStorage:getDataDir() .. "/aidoku/settings"
    util.makePath(self.sources_dir)
    util.makePath(self.downloads_dir)
    util.makePath(self.settings_dir)

    -- self.path is set by KOReader's plugin loader to this plugin's own
    -- directory; bin_arch (see binArch() above) names match the targets
    -- koreader/build.sh produces ("armv6"/"armv7" for Kindles/Kobos,
    -- "arm64"/"x64" for aarch64/x86-64 devices and desktops).
    local bin_arch = binArch()
    self.store = Store.new()
    self.engine = Engine.new(self.path .. "/bin/" .. bin_arch .. "/aidoku-run", function()
        local concurrency = self.store:networkConcurrency()
        return {
            FLARESOLVERR_HOST = self.store:flareSolverrHost(),
            AIDOKU_SETTINGS_DIR = self.settings_dir,
            NETWORK_CONCURRENCY = concurrency > 0 and tostring(concurrency) or "",
        }
    end)
    self.downloads_engine = DownloadsEngine.new(self.path .. "/bin/" .. bin_arch .. "/aidoku-downloads", self.downloads_dir)

    self:onDispatcherRegisterActions()
    self.ui.menu:registerToMainMenu(self)
    self:hookEndOfBook()
    self:hookShowFileManager()
    self:openPendingLibrary()
end

-- markCurrentChapterRead flags self.document's chapter as read (see
-- store.lua's read_chapters), independent of next_chapter_mode -- unlike
-- nextchapter.lua's own byPath lookup, this must run even when
-- auto-advance is off, so it's a separate lookup rather than something
-- threaded through NextChapter.handle(). downloads_engine:byPath() is a
-- subprocess call, so it needs its own Trapper:wrap() coroutine here (see
-- the note on this in downloadsengine.lua/nextchapter.lua) -- it can't run
-- inline in the onEndOfBook monkey-patch below.
function Aidoku:markCurrentChapterRead()
    local file = self.document and self.document.file
    if not file or file:sub(1, #self.downloads_dir) ~= self.downloads_dir then
        return
    end
    Trapper:wrap(function()
        local entry = self.downloads_engine:byPath(file)
        if entry then
            self.store:markChapterRead(entry.sourceKey, entry.mangaKey, entry.chapterKey)
        end
    end)
end

-- hookEndOfBook wires up auto-advance to the next chapter. It only applies
-- (and self.document is only set at all) when this plugin instance is
-- running inside ReaderUI, not the FileManager -- see the note on
-- is_doc_only in frontend/apps/filemanager/filemanager.lua vs
-- frontend/apps/reader/readerui.lua, which instantiate every plugin either
-- way but only the latter passes a document.
--
-- ReaderStatus's own onEndOfBook (frontend/apps/reader/modules/
-- readerstatus.lua) is registered before plugins are and never returns
-- true, so KOReader's event propagation (which stops at the first true --
-- see WidgetContainer:propagateEvent) can't be used to suppress its "end of
-- document" pop-up by registering a same-named handler here: ReaderStatus
-- would always run first regardless, and both would show. Instead this
-- wraps the ReaderStatus instance's own method directly: ours runs first,
-- and falls through to the original when the document isn't one of ours.
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

-- hookShowFileManager makes every "back to Files" path -- ReaderUI:onHome()
-- (dispatcher's "filemanager" action / readerback.lua's back-gesture-stack-
-- exhausted case), ReaderStatus:openFileBrowser() (the end-of-document
-- pop-up's "File browser" button and every end_document_action that lands
-- there), and readermenu.lua's top-bar file-browser icon (its callback
-- calls self.ui:onClose() + self.ui:showFileManager(file) directly, not
-- through onHome) -- return to Aidoku's own Library screen instead of
-- leaving the user looking at KOReader's raw file browser, when the
-- chapter being closed is one of ours. All three (and every other
-- showFileManager caller in readerui.lua) funnel through
-- ReaderUI:showFileManager(file) itself, so hooking that one method covers
-- all of them instead of chasing each call site separately.
--
-- Every caller does self:onClose() (tearing down this very plugin
-- instance) then self:showFileManager(file), which spins up a *new*
-- FileManager instance -- and since is_doc_only = false, a *new* Aidoku
-- plugin instance for it. There's no direct way to hand that future
-- instance a "reopen the Library" instruction, so this leaves a note in
-- pendinglibrary.lua (a require()-cached table, shared process-wide across
-- the teardown/recreate boundary) for that instance's init() to act on --
-- see openPendingLibrary() below. Same monkey-patch technique as
-- hookEndOfBook, for the same reason: showFileManager is a plain instance
-- method, not something event propagation can intercept.
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

function Aidoku:onAidokuBrowseSources()
    if not self.engine:isAvailable() or not self.downloads_engine:isAvailable() then
        UIManager:show(InfoMessage:new{
            text = _("The bundled aidoku-run/aidoku-downloads binaries were not found. Run koreader/build.sh in the aidokurunner-go repo to build and bundle them before using this plugin."),
        })
        return
    end

    local LibraryBrowser = require("librarybrowser")
    UIManager:show(LibraryBrowser:new{
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
