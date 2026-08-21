--[[--
Lists installed .aix sources in a sources directory, as
{text = "<display name> (<languages>)", name = "<display name>",
path = "<sources_dir>/<file>.aix", key = "<source's manifest id>"}
entries -- shared by sourcesbrowser.lua (one source, uses text),
globalsearchbrowser.lua (every source at once, uses name -- a result row
already carries the manga title, so repeating each source's full
language list per result there is just noise), and librarybrowser.lua
(resolving a bookmark's stored key back to a live path via findByKey).

Both key and text come straight from the source's own source.json (via
engine:manifest(), which reads it without loading the source's WASM module
-- see cmd/aidoku-run's `manifest` command), not guessed from the
installed file's name: the manifest id is the ground truth for identity
(and, unlike a filename convention, can't go stale if a source ever stops
following one), and the filename never had a human-readable name to show
in the first place. This costs one cheap subprocess call per installed
source on every list() -- no caching, since re-reading is simple and a
source's manifest can't change without its file changing too.
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
            table.insert(sources, {
                text = displayText(name, manifest),
                name = name,
                path = path,
                key = key,
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
