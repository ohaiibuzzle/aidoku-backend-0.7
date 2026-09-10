--[[--
Lists installed .aix sources in a sources directory, as
{text = "<display name> (<languages>)", mandatory = "v<version>",
name = "<display name>", path = "<sources_dir>/<file>.aix",
key = "<source's manifest id>", version = <int, or nil if unreadable>}
entries -- shared by sourcesbrowser.lua, globalsearchbrowser.lua,
librarybrowser.lua (findByKey), and repobrowser.lua.

key/text come from engine:manifest() (reads source.json without loading
WASM), not guessed from the filename -- the manifest id is the ground
truth for identity and can't go stale. One subprocess call per source per
list(), no caching, since a manifest can't change without its file
changing too.
]]

local lfs = require("libs/libkoreader-lfs")

local InstalledSources = {}

-- displayName falls back to the bare filename if the manifest couldn't be
-- read (e.g. a corrupt or unreadable file) so a broken source still shows
-- up as *something* rather than silently vanishing from the list.
local function displayName(filename, manifest)
    if not manifest or type(manifest.name) ~= "string" or manifest.name == "" then
        return filename:gsub("%.aix$", "")
    end
    return manifest.name
end

-- displayText appends "(<languages>)" (matching repobrowser.lua's own
-- repository-listing convention) to name.
local function displayText(name, manifest)
    local langs = manifest and type(manifest.languages) == "table" and table.concat(manifest.languages, ", ") or ""
    return langs ~= "" and (name .. " (" .. langs .. ")") or name
end

function InstalledSources.list(sources_dir, engine)
    local sources = {}
    for entry in lfs.dir(sources_dir) do
        if entry:match("%.aix$") then
            local path = sources_dir .. "/" .. entry
            local manifest = engine:manifest(path)
            -- Fallback for a manifest that couldn't be read: the bare
            -- filename, matching displayName()'s own fallback -- no worse
            -- than treating the whole filename as the identity, which is
            -- what every source's identity amounted to before source_key
            -- existed.
            local key = entry:gsub("%.aix$", "")
            if manifest and type(manifest.key) == "string" and manifest.key ~= "" then
                key = manifest.key
            end
            local name = displayName(entry, manifest)
            local version = manifest and manifest.version
            table.insert(sources, {
                text = displayText(name, manifest),
                mandatory = version and ("v" .. tostring(version)) or nil,
                name = name,
                path = path,
                key = key,
                version = version,
            })
        end
    end
    table.sort(sources, function(a, b) return a.text < b.text end)
    return sources
end

-- findByKey resolves a source_key (as persisted in a library bookmark or
-- downloads index entry) back to its currently-installed {text, path, key}
-- entry, or nil if no installed source currently has that key -- meaning
-- either it was removed, or its manifest id itself changed (unlike a
-- filename's version suffix, a source's id is not expected to change
-- across an update, so this shouldn't normally happen).
function InstalledSources.findByKey(sources_dir, engine, source_key)
    for _, source in ipairs(InstalledSources.list(sources_dir, engine)) do
        if source.key == source_key then
            return source
        end
    end
    return nil
end

return InstalledSources
