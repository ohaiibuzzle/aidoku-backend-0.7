--[[--
Generic "shell out to a bundled binary, decode JSON stdout" runner, shared
by engine.lua (aidoku-run) and downloadsengine.lua (aidoku-downloads) so the
Trapper/shell-quoting/stderr-capture plumbing exists in exactly one place.

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

local Runner = {}
Runner.__index = Runner

-- stderr_log_name should be distinct per bundled binary sharing this
-- module, so two Runners can't stomp on each other's captured stderr if
-- calls happen to overlap.
function Runner.new(bin_path, stderr_log_name)
    return setmetatable({
        bin_path = bin_path,
        stderr_log_name = stderr_log_name or "aidoku-stderr.log",
    }, Runner)
end

-- isAvailable reports whether the bundled binary exists at bin_path (it
-- won't until koreader/build.sh has cross-compiled and bundled it).
function Runner:isAvailable()
    return lfs.attributes(self.bin_path, "mode") == "file"
end

local function shellQuote(s)
    local escaped = tostring(s):gsub("'", "'\\''")
    return "'" .. escaped .. "'"
end
Runner.shellQuote = shellQuote

-- command_exists_cache memoizes commandExists() results for the life of the
-- process -- a device doesn't gain or lose binaries mid-session, and this
-- can otherwise run once per download.
local command_exists_cache = {}

-- commandExists shells out to POSIX "command -v" to check whether name is
-- on PATH, e.g. before relying on an optional tool like "nice" that isn't
-- guaranteed present on every device (busybox-based Kindle/Kobo firmware
-- ships a much smaller tool set than desktop Linux).
local function commandExists(name)
    if command_exists_cache[name] == nil then
        local found = false
        local f = io.popen("command -v " .. shellQuote(name) .. " >/dev/null 2>&1 && echo yes")
        if f then
            found = f:read("*l") == "yes"
            f:close()
        end
        command_exists_cache[name] = found
    end
    return command_exists_cache[name]
end

-- buildCommand assembles the full shell command line for args: env
-- assignments and an optional low-priority prefix ahead of the quoted
-- binary and its (also quoted) arguments, stderr redirected to a capture
-- file. Returns the command string and the stderr path it redirects to.
--
-- low_priority, if true and "nice" is available (see commandExists()),
-- runs the binary under "nice -n 19" so a background op (chapter prefetch)
-- writing a CBZ doesn't steal CPU from whatever the reader is doing in the
-- foreground. Checked at runtime rather than assumed, and skipped silently
-- if absent, rather than "ionice"-style unconditional prefixing -- a
-- missing tool in the prefix would fail the whole command, not just leave
-- it at normal priority.
local function buildCommand(bin_path, stderr_path, args, env, low_priority)
    local parts = {}
    if env then
        for k, v in pairs(env) do
            table.insert(parts, k .. "=" .. shellQuote(v))
        end
    end
    if low_priority and commandExists("nice") then
        table.insert(parts, "nice")
        table.insert(parts, "-n")
        table.insert(parts, "19")
    end
    table.insert(parts, shellQuote(bin_path))
    for _, a in ipairs(args) do
        table.insert(parts, shellQuote(a))
    end
    table.insert(parts, "2>" .. shellQuote(stderr_path))
    -- Trailing "; echo" guarantees at least one byte of output even if the
    -- command itself prints nothing, per Trapper:dismissablePopen()'s own
    -- advice -- otherwise a silent failure can't be told apart from one
    -- that hasn't produced output yet.
    return table.concat(parts, " ") .. "; echo"
end

-- exec runs the binary with args, returning trimmed stdout on success or
-- nil plus an error message (stderr, if any was captured, else a generic
-- message) on failure/cancellation. env, if given, is a table of
-- {VAR = value} exported into the command's environment (e.g.
-- FLARESOLVERR_HOST). See buildCommand() for low_priority.
function Runner:exec(args, progress_text, env, low_priority)
    local stderr_path = DataStorage:getDataDir() .. "/cache/" .. self.stderr_log_name
    local cmd = buildCommand(self.bin_path, stderr_path, args, env, low_priority)

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
            err_text = _("command produced no output")
        end
        logger.warn("aidoku: command failed:", cmd, err_text)
        return nil, err_text
    end
    return output
end

-- execJSON is exec() plus decoding stdout as JSON.
function Runner:execJSON(args, progress_text, env)
    local output, err = self:exec(args, progress_text, env)
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

return Runner
