--[[--
Lets the user set search filter values for one source (sort/select/
multi-select/check only -- text/range/note filters show as unsupported and
are skipped). Values live in self.values, keyed by filter ID, shaped for
aidoku-run's `search` filters.json argument -- engine.lua's Engine:search
builds that file from self.values.

CAUTION: the "check" filter value convention (1/-1/0 = include/exclude/
unset) is UNVERIFIED against a real compiled source; verify before relying
on it.
]]

local Menu = require("ui/widget/menu")
local OpenWidgets = require("openwidgets")
local UIManager = require("ui/uimanager")
local T = require("ffi/util").template
local _ = require("gettext")

local FilterBrowser = Menu:extend{
    title = _("Search filters"),
}

local SUPPORTED_KINDS = { sort = true, select = true, ["multi-select"] = true, check = true }

-- Filter.Title and Filter.CheckName are nilable Go *string fields, which
-- decode as KOReader json's null-as-function sentinel (not Lua nil) when
-- absent -- confirmed live against WeebCentral's own "sort" filter, which
-- has no Title. `x or fallback` would treat that sentinel as truthy and use
-- it as-is (rendering a function value), so this needs an explicit
-- type check rather than a plain `or`, same as elsewhere in the plugin.
local function filterLabel(filter)
    if type(filter.Title) == "string" and filter.Title ~= "" then
        return filter.Title
    end
    return filter.ID
end

-- array returns t if it's a real table (not KOReader json's null sentinel,
-- and not absent), or an empty table otherwise -- for safely iterating
-- nilable slice fields like Filter.Select.IDs (confirmed null in practice:
-- WeebCentral's "genre" filter has Options but no IDs).
local function array(t)
    return type(t) == "table" and t or {}
end

function FilterBrowser:init()
    self.filters = {}
    for _, f in ipairs(self.all_filters or {}) do
        if SUPPORTED_KINDS[f.Kind] then
            table.insert(self.filters, f)
        end
    end
    self.values = self.initial_values or {}
    self.item_table = {}
    Menu.init(self)
    OpenWidgets.push(self)
    self:refresh()
end

-- See librarybrowser.lua's onCloseWidget for why this is needed.
function FilterBrowser:onCloseWidget()
    Menu.onCloseWidget(self)
    OpenWidgets.remove(self)
end

-- --- summaries ---

local function sortSummary(filter, value)
    if not value then
        return T(_("Sort: %1"), _("default"))
    end
    local option = array(filter.SortOptions)[value.sortIndex + 1] or value.sortIndex
    return T(_("Sort: %1 (%2)"), option, value.sortAscending and _("ascending") or _("descending"))
end

local function selectSummary(filter, value)
    if not value or not value.value or value.value == "" then
        return T(_("%1: %2"), filterLabel(filter), _("any"))
    end
    return T(_("%1: %2"), filterLabel(filter), value.value)
end

local function multiselectSummary(filter, value)
    local n_in = value and #value.included or 0
    local n_ex = value and #value.excluded or 0
    if n_in == 0 and n_ex == 0 then
        return T(_("%1: %2"), filterLabel(filter), _("any"))
    end
    return T(_("%1: %2 included, %3 excluded"), filterLabel(filter), n_in, n_ex)
end

local function checkSummary(filter, value)
    local label = (type(filter.CheckName) == "string" and filter.CheckName ~= "") and filter.CheckName or filterLabel(filter)
    if not value or value.checkValue == 0 then
        return T(_("%1: %2"), label, _("unset"))
    end
    return T(_("%1: %2"), label, value.checkValue > 0 and _("yes") or _("no"))
end

function FilterBrowser:summary(filter)
    local value = self.values[filter.ID]
    if filter.Kind == "sort" then
        return sortSummary(filter, value)
    elseif filter.Kind == "select" then
        return selectSummary(filter, value)
    elseif filter.Kind == "multi-select" then
        return multiselectSummary(filter, value)
    else -- "check"
        return checkSummary(filter, value)
    end
end

function FilterBrowser:refresh()
    local item_table = {}
    for _, filter in ipairs(self.filters) do
        table.insert(item_table, { text = self:summary(filter), filter = filter })
    end
    table.insert(item_table, { text = _("Clear all filters"), is_clear = true })
    table.insert(item_table, { text = _("Search with these filters"), is_apply = true })
    self.item_table = item_table
    self:updateItems()
end

-- --- per-kind pickers ---

function FilterBrowser:pickSort(filter)
    local rows = {}
    local current = self.values[filter.ID]
    for i, option in ipairs(array(filter.SortOptions)) do
        local index = i - 1
        local marker = ""
        if current and current.sortIndex == index then
            marker = current.sortAscending and "▲ " or "▼ "
        end
        table.insert(rows, { text = marker .. option, index = index })
    end
    local picker
    picker = Menu:new{
        title = T(_("Sort: %1"), filterLabel(filter)),
        item_table = rows,
        is_popout = false,
        is_borderless = true,
        title_bar_fm_style = true,
        onMenuSelect = function(_menu, item)
            self.values[filter.ID] = { type = "sort", id = filter.ID, sortIndex = item.index, sortAscending = false }
            UIManager:close(picker)
            self:refresh()
            return true
        end,
        onMenuHold = function(_menu, item)
            self.values[filter.ID] = { type = "sort", id = filter.ID, sortIndex = item.index, sortAscending = true }
            UIManager:close(picker)
            self:refresh()
            return true
        end,
        onCloseWidget = function(self_picker)
            Menu.onCloseWidget(self_picker)
            OpenWidgets.remove(self_picker)
        end,
    }
    OpenWidgets.push(picker)
    UIManager:show(picker)
end

function FilterBrowser:pickSelect(filter)
    local rows = { { text = _("Any"), value = "" } }
    local current = self.values[filter.ID]
    for i, option in ipairs(array(filter.Select.Options)) do
        local id = array(filter.Select.IDs)[i] or option
        local marker = (current and current.value == id) and "✓ " or ""
        table.insert(rows, { text = marker .. option, value = id })
    end
    local picker
    picker = Menu:new{
        title = filterLabel(filter),
        item_table = rows,
        is_popout = false,
        is_borderless = true,
        title_bar_fm_style = true,
        onMenuSelect = function(_menu, item)
            if item.value == "" then
                self.values[filter.ID] = nil
            else
                self.values[filter.ID] = { type = "select", id = filter.ID, value = item.value }
            end
            UIManager:close(picker)
            self:refresh()
            return true
        end,
        onCloseWidget = function(self_picker)
            Menu.onCloseWidget(self_picker)
            OpenWidgets.remove(self_picker)
        end,
    }
    OpenWidgets.push(picker)
    UIManager:show(picker)
end

-- Cycles one option through not-selected -> included -> (excluded, if the
-- filter allows it) -> not-selected, without closing the picker, so the
-- user can toggle several options in one visit.
function FilterBrowser:pickMultiselect(filter)
    local function state(included, excluded, id)
        for _, v in ipairs(included) do
            if v == id then return "included" end
        end
        for _, v in ipairs(excluded) do
            if v == id then return "excluded" end
        end
        return "none"
    end

    local function buildRows()
        local current = self.values[filter.ID] or { included = {}, excluded = {} }
        local rows = {}
        for i, option in ipairs(array(filter.MultiSelect.Options)) do
            local id = array(filter.MultiSelect.IDs)[i] or option
            local s = state(current.included, current.excluded, id)
            local marker = s == "included" and "✓ " or (s == "excluded" and "✗ " or "")
            table.insert(rows, { text = marker .. option, id = id })
        end
        return rows
    end

    local picker
    local function cycle(id)
        local current = self.values[filter.ID] or { type = "multi-select", id = filter.ID, included = {}, excluded = {} }
        local s = state(current.included, current.excluded, id)
        local included, excluded = {}, {}
        for _, v in ipairs(current.included) do
            if v ~= id then table.insert(included, v) end
        end
        for _, v in ipairs(current.excluded) do
            if v ~= id then table.insert(excluded, v) end
        end
        if s == "none" then
            table.insert(included, id)
        elseif s == "included" and filter.MultiSelect.CanExclude then
            table.insert(excluded, id)
        end
        -- else ("excluded", or "included" without CanExclude): leave cleared
        current.included, current.excluded = included, excluded
        if #included == 0 and #excluded == 0 then
            self.values[filter.ID] = nil
        else
            self.values[filter.ID] = current
        end
        picker.item_table = buildRows()
        picker:updateItems()
        self:refresh()
    end

    picker = Menu:new{
        title = filterLabel(filter),
        item_table = buildRows(),
        is_popout = false,
        is_borderless = true,
        title_bar_fm_style = true,
        onMenuSelect = function(_menu, item)
            cycle(item.id)
            return true
        end,
        onCloseWidget = function(self_picker)
            Menu.onCloseWidget(self_picker)
            OpenWidgets.remove(self_picker)
        end,
    }
    OpenWidgets.push(picker)
    UIManager:show(picker)
end

function FilterBrowser:cycleCheck(filter)
    local current = self.values[filter.ID]
    local value = current and current.checkValue or 0
    if value == 0 then
        value = 1
    elseif value > 0 and filter.CheckCanExclude then
        value = -1
    else
        value = 0
    end
    if value == 0 then
        self.values[filter.ID] = nil
    else
        self.values[filter.ID] = { type = "check", id = filter.ID, checkValue = value }
    end
    self:refresh()
end

function FilterBrowser:onMenuSelect(item)
    if item.is_clear then
        self.values = {}
        self:refresh()
        return true
    end
    if item.is_apply then
        UIManager:close(self)
        if self.on_apply then
            -- self.values is a map (filter ID -> value table); on_apply gets
            -- it as-is so the caller can both pass it straight back in as
            -- the next initial_values and, separately, build the []FilterValue
            -- array engine:search() wants via pairs(values).
            self.on_apply(self.values)
        end
        return true
    end
    local filter = item.filter
    if filter.Kind == "sort" then
        self:pickSort(filter)
    elseif filter.Kind == "select" then
        self:pickSelect(filter)
    elseif filter.Kind == "multi-select" then
        self:pickMultiselect(filter)
    else -- "check"
        self:cycleCheck(filter)
    end
    return true
end

return FilterBrowser
