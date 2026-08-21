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
    util.makePath(self.sources_dir)
    util.makePath(self.downloads_dir)

    -- self.path is set by KOReader's plugin loader to this plugin's own
    -- directory. jit.arch (LuaJIT, which KOReader always runs on) names
    -- match Go's GOARCH for the targets koreader/build.sh produces ("arm"
    -- for armv7 Kindles/Kobos, "arm64" for aarch64 devices/desktops).
    self.engine = Engine.new(self.path .. "/bin/" .. jit.arch .. "/aidoku-run")
    self.store = Store.new()
    self.repo_url = DEFAULT_REPO_URL

    self:onDispatcherRegisterActions()
    self.ui.menu:registerToMainMenu(self)
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
    if not self.engine:isAvailable() then
        UIManager:show(InfoMessage:new{
            text = _("The bundled aidoku-run binary was not found. Run koreader/build.sh in the aidokurunner-go repo to build and bundle it before using this plugin."),
        })
        return
    end

    local LibraryBrowser = require("librarybrowser")
    UIManager:show(LibraryBrowser:new{
        engine = self.engine,
        store = self.store,
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
