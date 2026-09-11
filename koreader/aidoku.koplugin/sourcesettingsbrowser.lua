--[[--
Renders one source's settings (the select/multiselect/toggle/text subset --
group/page/stepper/segment/range/login/button/link/editable-list/picker/
custom are out of scope for now, see the plan this was built from) as a
flat list, recursing into groups with a "Group: Setting" label prefix rather
than a separate nested screen, since the only groups our current test
sources produce (source/settings.go's synthesized language/base-URL
pickers) are one level deep. Each row's current value is fetched once at
load via engine:settingsGet (one subprocess call per setting -- fine for the
handful of settings a source typically has, unlike a chapter list).
]]

local InputDialog = require("ui/widget/inputdialog")
local Menu = require("ui/widget/menu")
local NetworkMgr = require("ui/network/manager")
local OpenWidgets = require("openwidgets")
local Trapper = require("ui/trapper")
local UIManager = require("ui/uimanager")
local T = require("ffi/util").template
local _ = require("gettext")

local SourceSettingsBrowser = Menu:extend{}

local SUPPORTED_TYPES = { select = true, ["multi-select"] = true, switch = true, text = true }

-- array returns t if it's a real table, or an empty table otherwise --
-- nilable slice fields (e.g. SelectSetting.Titles, when a source's manifest
-- omits it) decode as KOReader json's null-as-function sentinel, not Lua
-- nil, so `t or {}` would treat that sentinel as truthy and hand back a
-- function instead -- see the identical note in filterbrowser.lua.
local function array(t)
    return type(t) == "table" and t or {}
end

-- flatten walks a Setting[] tree (as decoded from aidoku-run's `settings`
-- command), recursing into groups/pages and returning a flat array of
-- {setting = <leaf>, label = <display label, "Group: Title" when nested>}
-- for just the supported leaf types.
local function flatten(settings, prefix, out)
    out = out or {}
    for _, setting in ipairs(array(settings)) do
        local label = prefix ~= "" and (prefix .. ": " .. setting.Title) or setting.Title
        if setting.Type == "group" and setting.Group then
            flatten(setting.Group.Items, setting.Title ~= "" and setting.Title or prefix, out)
        elseif setting.Type == "page" and setting.Page then
            flatten(setting.Page.Items, setting.Title ~= "" and setting.Title or prefix, out)
        elseif SUPPORTED_TYPES[setting.Type] then
            table.insert(out, { setting = setting, label = label })
        end
    end
    return out
end

function SourceSettingsBrowser:init()
    self.title = T(_("%1 settings"), self.source_text or "")
    self.entries = {}
    self.values = {}
    self.item_table = {}
    Menu.init(self)
    OpenWidgets.push(self)
    UIManager:nextTick(function() self:reload() end)
end

-- See librarybrowser.lua's onCloseWidget for why this is needed.
function SourceSettingsBrowser:onCloseWidget()
    Menu.onCloseWidget(self)
    OpenWidgets.remove(self)
end

function SourceSettingsBrowser:reload()
    NetworkMgr:runWhenConnected(function()
        Trapper:wrap(function()
            local schema, err = self.engine:settingsList(self.source_path)
            if not schema then
                UIManager:show(require("ui/widget/infomessage"):new{ text = T(_("Could not load settings:\n%1"), err) })
                return
            end
            self.entries = flatten(schema, "")
            self.values = {}
            for _, entry in ipairs(self.entries) do
                self.values[entry.setting.Key] = self.engine:settingsGet(self.source_path, entry.setting.Key)
            end
            self:refresh()
        end)
    end)
end

-- summary formats entries.values[key] (a decoded JSON value -- boolean,
-- string, array of strings, or KOReader json's null-as-function sentinel
-- for "unset", see the note on this throughout the plugin) for display.
local function summary(setting, value)
    if setting.Type == "switch" then
        return (value == true) and _("On") or _("Off")
    elseif setting.Type == "multi-select" then
        if type(value) == "table" and #value > 0 then
            return table.concat(value, ", ")
        end
        return _("none")
    else -- "select" or "text"
        if type(value) == "string" and value ~= "" then
            return value
        end
        return _("not set")
    end
end

function SourceSettingsBrowser:refresh()
    local item_table = {}
    -- Not `for _, entry in ipairs(...)`: `_` is also this file's gettext
    -- local, and a loop variable of the same name shadows it for the rest
    -- of this function's body -- calling _(...) below would try to call the
    -- loop's integer index instead. Confirmed live: this crashed on
    -- MangaDex's settings screen with "attempt to call local '_' (a number
    -- value)".
    for _idx, entry in ipairs(self.entries) do
        local value = self.values[entry.setting.Key]
        table.insert(item_table, {
            text = T(_("%1: %2"), entry.label, summary(entry.setting, value)),
            entry = entry,
        })
    end
    if #item_table == 0 then
        table.insert(item_table, { text = _("This source has no configurable settings."), dim = true, is_placeholder = true })
    end
    self.item_table = item_table
    self:updateItems()
end

function SourceSettingsBrowser:setValue(key, cli_type, values, display_value)
    Trapper:wrap(function()
        local _res, err = self.engine:settingsSet(self.source_path, key, cli_type, values)
        if err and not _res then
            UIManager:show(require("ui/widget/infomessage"):new{ text = T(_("Could not save setting:\n%1"), err) })
            return
        end
        self.values[key] = display_value
        self:refresh()
    end)
end

function SourceSettingsBrowser:pickSelect(entry)
    local setting = entry.setting
    local rows = {}
    for i, option in ipairs(array(setting.Select.Values)) do
        local display = array(setting.Select.Titles)[i] or option
        table.insert(rows, { text = display, value = option })
    end
    local picker
    picker = Menu:new{
        title = entry.label,
        item_table = rows,
        is_popout = false,
        is_borderless = true,
        title_bar_fm_style = true,
        onMenuSelect = function(_menu, item)
            UIManager:close(picker)
            self:setValue(setting.Key, "select", { item.value }, item.value)
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

function SourceSettingsBrowser:pickMultiselect(entry)
    local setting = entry.setting

    local function isSelected(selected, option)
        for _, v in ipairs(selected) do
            if v == option then return true end
        end
        return false
    end

    local function buildRows(selected)
        local rows = {}
        for i, option in ipairs(array(setting.Multiselect.Values)) do
            local display = array(setting.Multiselect.Titles)[i] or option
            local marker = isSelected(selected, option) and "✓ " or ""
            table.insert(rows, { text = marker .. display, value = option })
        end
        return rows
    end

    local selected = {}
    local current = self.values[setting.Key]
    if type(current) == "table" then
        for _, v in ipairs(current) do table.insert(selected, v) end
    end

    local picker
    picker = Menu:new{
        title = entry.label,
        item_table = buildRows(selected),
        is_popout = false,
        is_borderless = true,
        title_bar_fm_style = true,
        onMenuSelect = function(_menu, item)
            if isSelected(selected, item.value) then
                local next_selected = {}
                for _, v in ipairs(selected) do
                    if v ~= item.value then table.insert(next_selected, v) end
                end
                selected = next_selected
            else
                table.insert(selected, item.value)
            end
            picker.item_table = buildRows(selected)
            picker:updateItems()
            self:setValue(setting.Key, "multiselect", selected, selected)
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

function SourceSettingsBrowser:promptText(entry)
    local setting = entry.setting
    local current = self.values[setting.Key]
    local dialog
    dialog = InputDialog:new{
        title = entry.label,
        input = type(current) == "string" and current or "",
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
                    local text = dialog:getInputText() or ""
                    UIManager:close(dialog)
                    self:setValue(setting.Key, "text", { text }, text)
                end,
            },
        }},
    }
    UIManager:show(dialog)
    dialog:onShowKeyboard()
end

function SourceSettingsBrowser:onMenuSelect(item)
    if item.is_placeholder then
        return true
    end
    local entry = item.entry
    local setting = entry.setting
    if setting.Type == "switch" then
        local value = self.values[setting.Key] == true
        self:setValue(setting.Key, "toggle", { value and "false" or "true" }, not value)
    elseif setting.Type == "select" then
        self:pickSelect(entry)
    elseif setting.Type == "multi-select" then
        self:pickMultiselect(entry)
    else -- "text"
        self:promptText(entry)
    end
    return true
end

return SourceSettingsBrowser
