--[[--
Prompts for a search query against one installed source and lists the
results. Tapping a result opens MangaBrowser on it; holding one bookmarks
(or unbookmarks) it to the library directly, without opening it.
]]

local InfoMessage = require("ui/widget/infomessage")
local InputDialog = require("ui/widget/inputdialog")
local Menu = require("ui/widget/menu")
local NetworkMgr = require("ui/network/manager")
local Trapper = require("ui/trapper")
local UIManager = require("ui/uimanager")
local T = require("ffi/util").template
local _ = require("gettext")

local SearchBrowser = Menu:extend{}

-- Manga.Title is a plain (non-pointer) Go string, so an absent title
-- decodes as "" rather than null -- and "" is truthy in Lua, so this needs
-- an explicit emptiness check rather than just `x or fallback`.
local function mangaLabel(manga)
    if type(manga.Title) == "string" and manga.Title ~= "" then
        return manga.Title
    end
    return manga.Key
end

-- Cover is a nilable Go *string; see the JSON-null note in mangabrowser.lua.
local function mangaCover(manga)
    return type(manga.Cover) == "string" and manga.Cover or nil
end

function SearchBrowser:init()
    self.item_table = {}
    self.page = 1
    self.query = nil
    self.available_filters = nil -- fetched once, see reloadFilters()
    self.filter_values = {} -- filter ID -> value table, see filterbrowser.lua
    Menu.init(self)
    -- init() runs during SearchBrowser:new{}, before the caller's own
    -- UIManager:show(self) -- showing the InputDialog here would get
    -- painted first and then buried under this Menu when the caller shows
    -- it next. Defer to the next tick so it shows on top instead.
    UIManager:nextTick(function()
        self:reloadFilters()
        self:promptQuery()
    end)
end

-- reloadFilters fetches this source's filters once (not re-fetched on every
-- search) so the "Filters…" row can appear immediately once results exist,
-- without a subprocess call per search.
function SearchBrowser:reloadFilters()
    local result = self.engine:filters(self.source_path)
    self.available_filters = type(result) == "table" and result or {}
end

function SearchBrowser:openFilters()
    local FilterBrowser = require("filterbrowser")
    UIManager:show(FilterBrowser:new{
        all_filters = self.available_filters,
        initial_values = self.filter_values,
        is_popout = false,
        is_borderless = true,
        title_bar_fm_style = true,
        on_apply = function(values)
            self.filter_values = values
            if self.query then
                self:runSearch(self.query)
            end
        end,
    })
end

function SearchBrowser:promptQuery()
    local dialog
    dialog = InputDialog:new{
        title = _("Search manga"),
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

function SearchBrowser:runSearch(query)
    self.query = query
    NetworkMgr:runWhenConnected(function()
        Trapper:wrap(function()
            local filters = {}
            for _, v in pairs(self.filter_values) do
                table.insert(filters, v)
            end
            local result, err = self.engine:search(self.source_path, query, self.page, filters)
            if not result then
                UIManager:show(InfoMessage:new{ text = T(_("Search failed:\n%1"), err) })
                return
            end
            local item_table = {}
            if self.available_filters and #self.available_filters > 0 then
                local n = 0
                for _ in pairs(self.filter_values) do n = n + 1 end
                local label = n > 0 and T(_("Filters (%1 set)…"), n) or _("Filters…")
                table.insert(item_table, { text = label, is_filters_entry = true })
            end
            -- result.Entries is a Go slice that's always non-nil (even when
            -- empty) here, but guard anyway -- see the null-decoding note
            -- in mangabrowser.lua.
            local entries = type(result.Entries) == "table" and result.Entries or {}
            for _, manga in ipairs(entries) do
                local label = mangaLabel(manga)
                if self.store:isBookmarked(self.source_key, manga.Key) then
                    label = "★ " .. label
                end
                table.insert(item_table, { text = label, manga = manga })
            end
            if #entries == 0 then
                UIManager:show(InfoMessage:new{ text = _("No results."), timeout = 2 })
            end
            self.item_table = item_table
            self:updateItems()
        end)
    end)
end

function SearchBrowser:onMenuSelect(item)
    if item.is_filters_entry then
        self:openFilters()
        return true
    end
    local MangaBrowser = require("mangabrowser")
    UIManager:show(MangaBrowser:new{
        engine = self.engine,
        store = self.store,
        downloads_engine = self.downloads_engine,
        ui = self.ui,
        source_path = self.source_path,
        source_key = self.source_key,
        downloads_dir = self.downloads_dir,
        manga = item.manga,
        is_popout = false,
        is_borderless = true,
        title_bar_fm_style = true,
    })
    return true
end

function SearchBrowser:onMenuHold(item)
    if item.is_filters_entry then
        return true
    end
    local manga = item.manga
    local label = mangaLabel(manga)
    local now_bookmarked
    if self.store:isBookmarked(self.source_key, manga.Key) then
        self.store:removeBookmark(self.source_key, manga.Key)
        now_bookmarked = false
    else
        self.store:addBookmark(self.source_key, self.source_path, manga.Key, label, mangaCover(manga))
        now_bookmarked = true
    end
    -- Update this row's marker in place rather than re-running the search.
    item.text = now_bookmarked and ("★ " .. label) or label
    self:updateItems()
    UIManager:show(InfoMessage:new{
        text = now_bookmarked and T(_("Added '%1' to library"), label) or T(_("Removed '%1' from library"), label),
        timeout = 1.5,
    })
    return true
end

return SearchBrowser
