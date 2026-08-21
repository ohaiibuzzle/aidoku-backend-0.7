--[[--
Prompts for a query and searches every installed source at once (in
sequence -- each call to engine:search shows its own brief progress widget
one after another), merging results into one list tagged by which source
each came from. A source that fails to search is skipped rather than
aborting the whole search; tapping a result opens MangaBrowser against its
actual source.
]]

local InfoMessage = require("ui/widget/infomessage")
local InputDialog = require("ui/widget/inputdialog")
local InstalledSources = require("installedsources")
local Menu = require("ui/widget/menu")
local NetworkMgr = require("ui/network/manager")
local Trapper = require("ui/trapper")
local UIManager = require("ui/uimanager")
local T = require("ffi/util").template
local _ = require("gettext")

local GlobalSearchBrowser = Menu:extend{
    title = _("Search all sources"),
}

-- Manga.Title is a plain (non-pointer) Go string, so an absent title
-- decodes as "" rather than null -- and "" is truthy in Lua, so this needs
-- an explicit emptiness check rather than just `x or fallback`.
local function mangaLabel(manga)
    if type(manga.Title) == "string" and manga.Title ~= "" then
        return manga.Title
    end
    return manga.Key
end

function GlobalSearchBrowser:init()
    self.item_table = {}
    Menu.init(self)
    -- init() runs during GlobalSearchBrowser:new{}, before the caller's own
    -- UIManager:show(self) -- see the same note in searchbrowser.lua's
    -- init() for why the query prompt must be deferred rather than shown
    -- here.
    UIManager:nextTick(function() self:promptQuery() end)
end

function GlobalSearchBrowser:promptQuery()
    local dialog
    dialog = InputDialog:new{
        title = _("Search all sources"),
        input_hint = _("Title…"),
        buttons = {{
            {
                text = _("Cancel"),
                id = "close",
                callback = function() UIManager:close(dialog) end,
            },
            {
                text = _("Search"),
                is_enter_default = true,
                callback = function()
                    local query = dialog:getInputText()
                    UIManager:close(dialog)
                    if query and query ~= "" then
                        self:runSearch(query)
                    end
                end,
            },
        }},
    }
    UIManager:show(dialog)
    dialog:onShowKeyboard()
end

function GlobalSearchBrowser:runSearch(query)
    NetworkMgr:runWhenConnected(function()
        Trapper:wrap(function()
            -- InstalledSources.list() now reads each installed source's
            -- manifest via a subprocess call (see installedsources.lua),
            -- so this has to happen inside the wrap too, not before it.
            local sources = InstalledSources.list(self.sources_dir, self.engine)
            if #sources == 0 then
                UIManager:show(InfoMessage:new{ text = _("No sources installed."), timeout = 2 })
                return
            end
            local item_table = {}
            local failed = {}
            for _, src in ipairs(sources) do
                local result = self.engine:search(src.path, query, 1)
                local entries = result and type(result.Entries) == "table" and result.Entries or nil
                if entries then
                    for _, manga in ipairs(entries) do
                        table.insert(item_table, {
                            text = "[" .. src.text .. "] " .. mangaLabel(manga),
                            manga = manga,
                            source_path = src.path,
                            source_key = src.key,
                        })
                    end
                else
                    table.insert(failed, src.text)
                end
            end
            self.item_table = item_table
            self:updateItems()
            if #item_table == 0 then
                UIManager:show(InfoMessage:new{ text = _("No results from any source."), timeout = 2 })
            elseif #failed > 0 then
                UIManager:show(InfoMessage:new{
                    text = T(_("Some sources failed to search: %1"), table.concat(failed, ", ")),
                    timeout = 3,
                })
            end
        end)
    end)
end

function GlobalSearchBrowser:onMenuSelect(item)
    local MangaBrowser = require("mangabrowser")
    UIManager:show(MangaBrowser:new{
        engine = self.engine,
        store = self.store,
        downloads_engine = self.downloads_engine,
        ui = self.ui,
        source_path = item.source_path,
        source_key = item.source_key,
        downloads_dir = self.downloads_dir,
        manga = item.manga,
        is_popout = false,
        is_borderless = true,
        title_bar_fm_style = true,
    })
    return true
end

return GlobalSearchBrowser
