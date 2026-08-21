--[[--
Lists the sources available from a repository index (index.min.json) and
installs the selected one's .aix into sources_dir. See repo/repo.go in the
parent Go repo for the format this talks to.
]]

local InfoMessage = require("ui/widget/infomessage")
local Menu = require("ui/widget/menu")
local NetworkMgr = require("ui/network/manager")
local Trapper = require("ui/trapper")
local UIManager = require("ui/uimanager")
local T = require("ffi/util").template
local _ = require("gettext")

local RepoBrowser = Menu:extend{
    title = _("Source repository"),
}

function RepoBrowser:init()
    self.item_table = {}
    Menu.init(self)
    -- init() runs during RepoBrowser:new{}, before the caller's own
    -- UIManager:show(self) -- NetworkMgr:runWhenConnected() calls back
    -- synchronously when already online, so without this the progress
    -- widget reload() shows would get painted first and buried under this
    -- Menu when the caller shows it next.
    UIManager:nextTick(function()
        NetworkMgr:runWhenConnected(function() self:reload() end)
    end)
end

function RepoBrowser:reload()
    Trapper:wrap(function()
        local index, err = self.engine:repoList(self.repo_url)
        if not index then
            UIManager:show(InfoMessage:new{ text = T(_("Could not load repository:\n%1"), err) })
            return
        end
        local item_table = {}
        -- See the null-decoding note in mangabrowser.lua: a repo entry with
        -- no sources, or a source with no languages, decodes those fields
        -- to a function sentinel rather than nil.
        local sources = type(index.sources) == "table" and index.sources or {}
        for _, src in ipairs(sources) do
            local src_langs = type(src.languages) == "table" and src.languages or {}
            local langs = table.concat(src_langs, ", ")
            table.insert(item_table, {
                text = langs ~= "" and (src.name .. " (" .. langs .. ")") or src.name,
                mandatory = "v" .. tostring(src.version),
                source = src,
            })
        end
        self.item_table = item_table
        self:updateItems()
    end)
end

function RepoBrowser:onMenuSelect(item)
    Trapper:wrap(function()
        local path, err = self.engine:repoInstall(self.repo_url, item.source.id, self.sources_dir)
        if not path then
            UIManager:show(InfoMessage:new{ text = T(_("Install failed:\n%1"), err) })
            return
        end
        if self.refresh_callback then
            self.refresh_callback()
        end
        UIManager:close(self)
        UIManager:show(InfoMessage:new{
            text = T(_("Installed %1"), item.source.name),
            timeout = 2,
        })
    end)
    return true
end

return RepoBrowser
