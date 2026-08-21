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
    Menu.init(self)
    -- init() runs during SearchBrowser:new{}, before the caller's own
    -- UIManager:show(self) -- showing the InputDialog here would get
    -- painted first and then buried under this Menu when the caller shows
    -- it next. Defer to the next tick so it shows on top instead.
    UIManager:nextTick(function() self:promptQuery() end)
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
    NetworkMgr:runWhenConnected(function()
        Trapper:wrap(function()
            local result, err = self.engine:search(self.source_path, query, self.page)
            if not result then
                UIManager:show(InfoMessage:new{ text = T(_("Search failed:\n%1"), err) })
                return
            end
            local item_table = {}
            -- result.Entries is a Go slice that's always non-nil (even when
            -- empty) here, but guard anyway -- see the null-decoding note
            -- in mangabrowser.lua.
            local entries = type(result.Entries) == "table" and result.Entries or {}
            for _, manga in ipairs(entries) do
                local label = mangaLabel(manga)
                if self.store:isBookmarked(self.source_path, manga.Key) then
                    label = "★ " .. label
                end
                table.insert(item_table, { text = label, manga = manga })
            end
            if #item_table == 0 then
                UIManager:show(InfoMessage:new{ text = _("No results."), timeout = 2 })
            end
            self.item_table = item_table
            self:updateItems()
        end)
    end)
end

function SearchBrowser:onMenuSelect(item)
    local MangaBrowser = require("mangabrowser")
    UIManager:show(MangaBrowser:new{
        engine = self.engine,
        store = self.store,
        ui = self.ui,
        source_path = self.source_path,
        downloads_dir = self.downloads_dir,
        manga = item.manga,
        is_popout = false,
        is_borderless = true,
        title_bar_fm_style = true,
    })
    return true
end

function SearchBrowser:onMenuHold(item)
    local manga = item.manga
    local label = mangaLabel(manga)
    local now_bookmarked
    if self.store:isBookmarked(self.source_path, manga.Key) then
        self.store:removeBookmark(self.source_path, manga.Key)
        now_bookmarked = false
    else
        self.store:addBookmark(self.source_path, manga.Key, label, mangaCover(manga))
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
