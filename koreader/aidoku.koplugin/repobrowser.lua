--[[--
Lists the sources available from a repository index (index.min.json) and
installs the selected one's .aix into sources_dir. See repo/repo.go in the
parent Go repo for the format this talks to.

Already-installed sources (matched against installedsources.lua by manifest
key, same identity a repo entry's own id shares) are surfaced to the top of
the list, and prefixed with "↑ " when the repo's version is newer than the
installed one -- same "prefix with a marker symbol" convention
globalsearchbrowser.lua uses for its "★ " bookmark marker.
]]

local InfoMessage = require("ui/widget/infomessage")
local InstalledSources = require("installedsources")
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
        -- Cross-reference against what's already installed (by manifest
        -- key, same identity repo_url's own src.id shares -- see the note
        -- on this in installedsources.lua) so an already-installed source
        -- can be surfaced to the top of the list and flagged when a newer
        -- version is available, rather than sitting wherever the repo
        -- index happens to order it.
        local installed_version_by_key = {}
        for _, installed in ipairs(InstalledSources.list(self.sources_dir, self.engine)) do
            installed_version_by_key[installed.key] = installed.version
        end
        -- See the null-decoding note in mangabrowser.lua: a repo entry with
        -- no sources, or a source with no languages, decodes those fields
        -- to a function sentinel rather than nil.
        local sources = type(index.sources) == "table" and index.sources or {}
        local installed_items, other_items = {}, {}
        for _, src in ipairs(sources) do
            local src_langs = type(src.languages) == "table" and src.languages or {}
            local langs = table.concat(src_langs, ", ")
            local text = langs ~= "" and (src.name .. " (" .. langs .. ")") or src.name
            local installed_version = installed_version_by_key[src.id]
            local is_installed = installed_version ~= nil
            -- Same "prefix with a marker symbol" convention as
            -- globalsearchbrowser.lua's "★ " bookmark marker.
            if is_installed and type(src.version) == "number" and src.version > installed_version then
                text = "↑ " .. text
            end
            local item = {
                text = text,
                mandatory = "v" .. tostring(src.version),
                source = src,
            }
            table.insert(is_installed and installed_items or other_items, item)
        end
        local item_table = {}
        for _, item in ipairs(installed_items) do
            table.insert(item_table, item)
        end
        for _, item in ipairs(other_items) do
            table.insert(item_table, item)
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
