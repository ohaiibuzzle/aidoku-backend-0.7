--[[--
Engine wraps the bundled aidoku-run binary: it shells out to it for every
call (see cmd/aidoku-run/main.go in the parent repo for the command surface)
and decodes its JSON stdout into Lua tables. See subprocess.lua for the
underlying "shell out and decode JSON" mechanics and its important note
about Trapper:wrap().
]]

local DataStorage = require("datastorage")
local Runner = require("subprocess")
local json = require("json")
local _ = require("gettext")

local Engine = {}
Engine.__index = Engine

-- env_fn, if given, is called on every source-running command (info,
-- search, mangaUpdate, download -- not repoList/repoInstall, which don't
-- load a source at all) to get a fresh {VAR = value} table exported into
-- the command's environment. Used for FLARESOLVERR_HOST, read from
-- store.lua's setting rather than baked in at construction time, so
-- changing it in Settings takes effect on the next call with no restart.
function Engine.new(bin_path, env_fn)
    return setmetatable({
        runner = Runner.new(bin_path, "aidoku-run-stderr.log"),
        env_fn = env_fn,
    }, Engine)
end

function Engine:isAvailable()
    return self.runner:isAvailable()
end

function Engine:sourceEnv()
    if self.env_fn then
        return self.env_fn()
    end
    return nil
end

-- The leading "-" on repo/cookie subcommands is a required but unused
-- placeholder for aidoku-run's <source-dir> positional argument -- see
-- handleRepo()/handleCookie() in cmd/aidoku-run/main.go, which special-case
-- these commands before a source is ever loaded.

function Engine:repoList(index_url)
    return self.runner:execJSON({ "-", "repo", "list", index_url }, _("Fetching repository…"))
end

function Engine:repoInstall(index_url, source_id, dest_dir)
    return self.runner:exec({ "-", "repo", "install", index_url, source_id, dest_dir }, _("Installing…"))
end

function Engine:info(source_path)
    return self.runner:execJSON({ source_path, "info" }, _("Loading source…"), self:sourceEnv())
end

-- manifest reads {key, name, version, languages} straight from an
-- installed source's source.json, without loading its WASM module -- see
-- cmd/aidoku-run's `manifest` command and source.ReadManifest in the
-- parent Go repo. Much cheaper than info() (which loads the source), so
-- this is what installedsources.lua uses to identify and display every
-- installed source, not just one.
function Engine:manifest(source_path)
    return self.runner:execJSON({ source_path, "manifest" }, false)
end

-- encodeFilterValues JSON-encodes filter_values (an array of tables shaped
-- like filterbrowser.lua's self.values entries), dropping any empty
-- included/excluded array first. KOReader's json module can't tell an
-- empty Lua table meant as an array from one meant as an object, so
-- json.encode({}) produces "{}" -- which then fails to decode into Go's
-- []string on the receiving end (confirmed live: selecting one multiselect
-- filter option, leaving the other side empty, made `search` fail with
-- "cannot unmarshal object into Go struct field ...excluded of type
-- []string"). Omitting the key entirely sidesteps the ambiguity, and
-- matches the Go side's own `omitempty` for these fields.
local function encodeFilterValues(filter_values)
    local sanitized = {}
    for i, v in ipairs(filter_values) do
        local copy = {}
        for k, val in pairs(v) do
            if not ((k == "included" or k == "excluded") and type(val) == "table" and #val == 0) then
                copy[k] = val
            end
        end
        sanitized[i] = copy
    end
    return json.encode(sanitized)
end

-- filter_values, if given, is an array of tables shaped like
-- filterbrowser.lua's self.values entries (id/type/value/sortIndex/
-- sortAscending/checkValue/included/excluded) -- written to a scratch JSON
-- file and passed as aidoku-run's `search` command's optional third
-- argument, since that's the only way to hand it a JSON body (no stdin
-- piping through Trapper:dismissablePopen()'s shell-command model).
function Engine:search(source_path, query, page, filter_values)
    local args = { source_path, "search", query, tostring(page or 1) }
    if filter_values and #filter_values > 0 then
        local filters_path = DataStorage:getDataDir() .. "/cache/aidoku-search-filters.json"
        local f = io.open(filters_path, "w")
        if f then
            f:write(encodeFilterValues(filter_values))
            f:close()
            table.insert(args, filters_path)
        end
    end
    return self.runner:execJSON(args, _("Searching…"), self:sourceEnv())
end

-- filters lists a source's static + dynamic search filters (see
-- filterbrowser.lua, which renders the sort/select/multi-select/check
-- subset of these).
function Engine:filters(source_path)
    return self.runner:execJSON({ source_path, "filters" }, _("Loading filters…"), self:sourceEnv())
end

-- settingsList returns a source's full static + dynamic settings schema
-- (see sourcesettingsbrowser.lua, which renders the select/multiselect/
-- toggle/text subset of these).
function Engine:settingsList(source_path)
    return self.runner:execJSON({ source_path, "settings" }, _("Loading settings…"), self:sourceEnv())
end

-- settingsGet returns the current value of one setting (key is the bare
-- setting key, without the sourceKey. prefix aidoku-run's settings store
-- namespaces it under internally) -- nil if neither an explicit override nor
-- a manifest default exists.
function Engine:settingsGet(source_path, key)
    local result, err = self.runner:execJSON({ source_path, "settings", "get", key }, false, self:sourceEnv())
    if not result then
        return nil, err
    end
    return result.value
end

-- settingsSet stores a setting value. setting_type is "toggle"/"select"/
-- "text"/"multiselect" (matching cmd/aidoku-run's settings-set usage), and
-- values is an array of one-or-more already-stringified values: a single
-- "true"/"false" for toggle, a single string for select/text (joined with
-- spaces on the Go side, so this should really only ever be one element),
-- or one element per selection for multiselect.
function Engine:settingsSet(source_path, key, setting_type, values)
    local args = { source_path, "settings", "set", key, setting_type }
    for _, v in ipairs(values) do
        table.insert(args, v)
    end
    return self.runner:exec(args, false, self:sourceEnv())
end

function Engine:mangaUpdate(source_path, manga_key)
    return self.runner:execJSON({ source_path, "manga", manga_key }, _("Loading chapters…"), self:sourceEnv())
end

-- downloads_dir, if given, is passed through to aidoku-run so it records
-- the download into the SQLite index there itself, in the same process
-- that writes the CBZ -- see the comment on this in
-- cmd/aidoku-downloads/main.go. Every call from this plugin should pass it;
-- it's optional only because the bare CLI is also used standalone/without
-- an index (see cmd/aidoku-run/main.go's own usage text).
--
-- silent (used for background chapter prefetch) passes false instead of a
-- progress string, which Trapper:dismissablePopen() turns into a fully
-- invisible, non-input-intercepting trap widget instead of a visible
-- "Downloading…" popup -- see ui/trapper.lua. It also runs the binary under
-- a lower CPU priority (see the note on this in subprocess.lua's exec()),
-- since a silent download is by definition a background prefetch, not
-- something the user is actively waiting on.
function Engine:download(source_path, manga_key, chapter_key, out_path, downloads_dir, silent)
    -- Not "silent and false or ...": that's the classic Lua and/or-ternary
    -- trap -- it misfires whenever the "true" branch value is itself falsy,
    -- which false always is.
    local progress_text = _("Downloading chapter…")
    if silent then
        progress_text = false
    end
    local args = { source_path, "download", manga_key, chapter_key, out_path }
    if downloads_dir then
        table.insert(args, downloads_dir)
    end
    return self.runner:exec(args, progress_text, self:sourceEnv(), silent)
end

return Engine
