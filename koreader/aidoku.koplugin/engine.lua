--[[--
Engine wraps the bundled aidoku-run binary: it shells out to it for every
call (see cmd/aidoku-run/main.go in the parent repo for the command surface)
and decodes its JSON stdout into Lua tables. See subprocess.lua for the
underlying "shell out and decode JSON" mechanics and its important note
about Trapper:wrap().
]]

local Runner = require("subprocess")
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

function Engine:search(source_path, query, page)
    return self.runner:execJSON({ source_path, "search", query, tostring(page or 1) }, _("Searching…"), self:sourceEnv())
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
-- "Downloading…" popup -- see ui/trapper.lua.
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
    return self.runner:exec(args, progress_text, self:sourceEnv())
end

return Engine
