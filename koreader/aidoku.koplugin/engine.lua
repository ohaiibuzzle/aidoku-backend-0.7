--[[--
Engine wraps the bundled aidoku-run binary: it shells out to it for every
call (see cmd/aidoku-run/main.go in the parent repo for the command surface)
and decodes its JSON stdout into Lua tables.

Every method here calls Trapper:dismissablePopen(), which must run inside a
coroutine started by Trapper:wrap() to show progress/allow cancellation --
see ui/trapper.lua. If called unwrapped it still works, falling back to a
plain blocking io.popen() with no progress UI (Trapper's own fallback).
Callers should wrap the whole user action (call + resulting UI update) in a
single Trapper:wrap(), not just the call, since Trapper:wrap() returns as
soon as the wrapped function first yields rather than when it finishes.
]]

local DataStorage = require("datastorage")
local Trapper = require("ui/trapper")
local json = require("json")
local lfs = require("libs/libkoreader-lfs")
local logger = require("logger")
local _ = require("gettext")

local Engine = {}
Engine.__index = Engine

function Engine.new(bin_path)
    return setmetatable({ bin_path = bin_path }, Engine)
end

-- isAvailable reports whether the bundled binary exists at bin_path (it
-- won't until koreader/build.sh has cross-compiled and bundled it).
function Engine:isAvailable()
    return lfs.attributes(self.bin_path, "mode") == "file"
end

local function shellQuote(s)
    local escaped = tostring(s):gsub("'", "'\\''")
    return "'" .. escaped .. "'"
end

-- exec runs the binary with args, returning trimmed stdout on success or
-- nil plus an error message (stderr, if any was captured, else a generic
-- message) on failure/cancellation.
function Engine:exec(args, progress_text)
    local stderr_path = DataStorage:getDataDir() .. "/cache/aidoku-run-stderr.log"
    local parts = { shellQuote(self.bin_path) }
    for _, a in ipairs(args) do
        table.insert(parts, shellQuote(a))
    end
    table.insert(parts, "2>" .. shellQuote(stderr_path))
    -- Trailing "; echo" guarantees at least one byte of output even if the
    -- command itself prints nothing, per Trapper:dismissablePopen()'s own
    -- advice -- otherwise a silent failure can't be told apart from one
    -- that hasn't produced output yet.
    local cmd = table.concat(parts, " ") .. "; echo"

    local completed, output = Trapper:dismissablePopen(cmd, progress_text)
    if not completed then
        return nil, _("Cancelled")
    end

    output = output and output:gsub("%s+$", "") or ""
    if output == "" then
        local err_text = ""
        local f = io.open(stderr_path, "r")
        if f then
            err_text = f:read("*a") or ""
            f:close()
        end
        err_text = err_text:gsub("%s+$", "")
        if err_text == "" then
            err_text = _("aidoku-run produced no output")
        end
        logger.warn("aidoku: command failed:", cmd, err_text)
        return nil, err_text
    end
    return output
end

-- execJSON is exec() plus decoding stdout as JSON.
function Engine:execJSON(args, progress_text)
    local output, err = self:exec(args, progress_text)
    if not output then
        return nil, err
    end
    local ok, decoded = pcall(json.decode, output)
    if not ok then
        logger.warn("aidoku: could not decode JSON output:", output)
        return nil, output
    end
    return decoded
end

-- The leading "-" on repo/cookie subcommands is a required but unused
-- placeholder for aidoku-run's <source-dir> positional argument -- see
-- handleRepo()/handleCookie() in cmd/aidoku-run/main.go, which special-case
-- these commands before a source is ever loaded.

function Engine:repoList(index_url)
    return self:execJSON({ "-", "repo", "list", index_url }, _("Fetching repository…"))
end

function Engine:repoInstall(index_url, source_id, dest_dir)
    return self:exec({ "-", "repo", "install", index_url, source_id, dest_dir }, _("Installing…"))
end

function Engine:info(source_path)
    return self:execJSON({ source_path, "info" }, _("Loading source…"))
end

function Engine:search(source_path, query, page)
    return self:execJSON({ source_path, "search", query, tostring(page or 1) }, _("Searching…"))
end

function Engine:mangaUpdate(source_path, manga_key)
    return self:execJSON({ source_path, "manga", manga_key }, _("Loading chapters…"))
end

function Engine:download(source_path, manga_key, chapter_key, out_path)
    return self:exec({ source_path, "download", manga_key, chapter_key, out_path }, _("Downloading chapter…"))
end

return Engine
