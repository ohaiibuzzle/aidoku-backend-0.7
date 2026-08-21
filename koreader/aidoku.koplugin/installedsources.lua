--[[--
Lists installed .aix sources in a sources directory, as
{text = "<id>-v<version>", path = "<sources_dir>/<id>-v<version>.aix"}
entries -- shared by sourcesbrowser.lua (one source) and
globalsearchbrowser.lua (every source at once).
]]

local lfs = require("libs/libkoreader-lfs")

local InstalledSources = {}

function InstalledSources.list(sources_dir)
    local sources = {}
    for entry in lfs.dir(sources_dir) do
        if entry:match("%.aix$") then
            table.insert(sources, {
                text = entry:gsub("%.aix$", ""),
                path = sources_dir .. "/" .. entry,
            })
        end
    end
    table.sort(sources, function(a, b) return a.text < b.text end)
    return sources
end

return InstalledSources
