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
local Store = require("store")

-- The Aidoku Community Sources repository (see https://aidoku-community.github.io/sources/).
local DEFAULT_REPO_URL = "https://aidoku-community.github.io/sources/index.min.json"

local Aidoku = WidgetContainer:extend{
    name = "aidoku",
    is_doc_only = false,
}

function Aidoku:init()
    self.sources_dir = DataStorage:getDataDir() .. "/aidoku/sources"
    self.downloads_dir = DataStorage:getDataDir() .. "/aidoku/downloads"
    self.settings_dir = DataStorage:getDataDir() .. "/aidoku/settings"
    util.makePath(self.sources_dir)
    util.makePath(self.downloads_dir)
    util.makePath(self.settings_dir)

    -- self.path is set by KOReader's plugin loader to this plugin's own
    -- directory. jit.arch (LuaJIT, which KOReader always runs on) names
    -- match Go's GOARCH for the targets koreader/build.sh produces ("arm"
    -- for armv7 Kindles/Kobos, "arm64" for aarch64 devices/desktops).
    self.store = Store.new()
    self.engine = Engine.new(self.path .. "/bin/" .. jit.arch .. "/aidoku-run", function()
        return {
            FLARESOLVERR_HOST = self.store:flareSolverrHost(),
            AIDOKU_SETTINGS_DIR = self.settings_dir,
        }
    end)
    self.downloads_engine = DownloadsEngine.new(self.path .. "/bin/" .. jit.arch .. "/aidoku-downloads", self.downloads_dir)
    self.repo_url = DEFAULT_REPO_URL

    self:onDispatcherRegisterActions()
    self.ui.menu:registerToMainMenu(self)
    self:hookEndOfBook()
    self:hookHome()
    self:hookStatusFileBrowser()
    self:openPendingLibrary()
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

-- hookHome makes the "Files" action (frontend/apps/reader/readerui.lua's
-- onHome -- bound to the dispatcher's "filemanager" action and to
-- readerback.lua's back-gesture-stack-exhausted case) return to Aidoku's
-- own Library screen instead of leaving the user looking at KOReader's raw
-- file browser, when the chapter being closed is one of ours.
--
-- ReaderUI:onHome() unconditionally does self:onClose() (tearing down this
-- very plugin instance) then self:showFileManager(file), which spins up a
-- *new* FileManager instance -- and since is_doc_only = false, a *new*
-- Aidoku plugin instance for it. There's no direct way to hand that future
-- instance a "reopen the Library" instruction, so this leaves a note in
-- pendinglibrary.lua (a require()-cached table, shared process-wide across
-- the teardown/recreate boundary) for that instance's init() to act on --
-- see openPendingLibrary() below. Same monkey-patch technique as
-- hookEndOfBook, for the same reason: onHome is a plain instance method,
-- not something event propagation can intercept.
function Aidoku:hookHome()
    if not self.document then
        return
    end
    local original_on_home = self.ui.onHome
    self.ui.onHome = function(ui_self, ...)
        local file = self.document and self.document.file
        if file and file:sub(1, #self.downloads_dir) == self.downloads_dir then
            require("pendinglibrary").open = true
        end
        return original_on_home(ui_self, ...)
    end
end

-- hookStatusFileBrowser covers the actual "Files" button users hit after
-- finishing a chapter: the end-of-document pop-up (readerstatus.lua's
-- onEndOfBook, which hookEndOfBook above suppresses only when auto-advance
-- handles the chapter transition) has a button literally labeled "File
-- browser". That button, plus every G_reader_settings end_document_action
-- that also lands in the file browser ("book_status_file_browser",
-- "file_browser", and the interrupted-quickstart-guide case), all funnel
-- through ReaderStatus:openFileBrowser() -- a separate call path from
-- ReaderUI:onHome() above, so it needs its own hook using the same
-- pendinglibrary.lua bridge.
function Aidoku:hookStatusFileBrowser()
    if not self.document or not self.ui.status then
        return
    end
    local original_open_file_browser = self.ui.status.openFileBrowser
    self.ui.status.openFileBrowser = function(status_self, ...)
        local file = self.document and self.document.file
        if file and file:sub(1, #self.downloads_dir) == self.downloads_dir then
            require("pendinglibrary").open = true
        end
        return original_open_file_browser(status_self, ...)
    end
end

-- Reopens the Library screen on top of a freshly shown FileManager when a
-- previous ReaderUI instance's hookHome()/hookStatusFileBrowser() (above)
-- requested it.
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
            text = _("Aidoku"),
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
        repo_url = self.repo_url,
        is_popout = false,
        is_borderless = true,
        title_bar_fm_style = true,
    })
end

return Aidoku
