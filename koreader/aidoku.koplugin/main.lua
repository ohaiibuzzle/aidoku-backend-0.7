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

-- dataSubdir builds an absolute-or-relative path under DataStorage's data
-- dir, normalized to never start with "./". DataStorage:getDataDir() isn't
-- guaranteed to return an absolute path -- confirmed on a real Kindle whose
-- launcher cd's into an absolute KOREADER_DIR and then execs a *relative*
-- "./reader.lua", where it returns the bare "." -- which would otherwise
-- make this "./aidoku/<name>". KOReader itself strips/never adds that "./"
-- when it records a document's own path (e.g. self.document.file, or a
-- .sdr's doc_path), so leaving it in self.downloads_dir would make every
-- `file:sub(1, #self.downloads_dir) == self.downloads_dir` prefix check
-- below (and in nextchapter.lua) silently never match -- which is exactly
-- what happened: read-chapter marking, next-chapter advance, and returning
-- to the Library after closing a book all silently no-op'd on that device,
-- with no error, because every single one of those checks is gated on this
-- same comparison. Stripping the "./" here (rather than fixing each
-- comparison site) fixes all of them at the source and is a no-op for I/O
-- itself -- "aidoku/downloads/x" and "./aidoku/downloads/x" name the same
-- file from the same working directory.
local function dataSubdir(name)
    return (DataStorage:getDataDir() .. "/aidoku/" .. name):gsub("^%./", "")
end

function Aidoku:init()
    self.sources_dir = dataSubdir("sources")
    self.downloads_dir = dataSubdir("downloads")
    self.settings_dir = dataSubdir("settings")
    util.makePath(self.sources_dir)
    util.makePath(self.downloads_dir)
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
    self.downloads_engine = DownloadsEngine.new(self.downloads_dir)

    self:onDispatcherRegisterActions()
    self.ui.menu:registerToMainMenu(self)
    self:hookEndOfBook()
    self:hookShowFileManager()
    self:hookClose()
    self:openPendingLibrary()
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

-- hookClose closes any Aidoku screen still open (see openwidgets.lua) right
-- before self.ui itself closes -- covering Exit, Restart, and the Reader<->
-- FileManager handoff alike, since all three route through onClose(). Each
-- Aidoku screen (LibraryBrowser, MangaBrowser, ...) is its own independent
-- UIManager top-level widget, not a child of self.ui -- drilling from one
-- screen into another only closes the screen being left, not its
-- ancestors (e.g. opening a chapter closes MangaBrowser but leaves
-- LibraryBrowser sitting on the window stack, just buried under the
-- Reader). UIManager's own run loop only stops once its window stack is
-- completely empty, so a screen left open when self.ui closes -- most
-- visibly, when Exit is invoked while reading -- silently blocks KOReader
-- from actually quitting: closing self.ui alone empties it down to just
-- that leftover screen, which then resurfaces on screen instead of
-- KOReader exiting, and has to be closed by hand before the app-level exit
-- can complete. Applies to both FileManager and ReaderUI instances (unlike
-- hookEndOfBook/hookShowFileManager, which are ReaderUI-only), since either
-- one closing can be the point where a leftover screen would otherwise be
-- exposed.
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

function Aidoku:onAidokuBrowseSources()
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
