--[[--
Generic "shell out to a bundled binary, decode JSON stdout" runner, used by
engine.lua to talk to aidoku-run. downloadsengine.lua and store.lua's
library/history methods do NOT use this -- they talk to their own SQLite
files directly via lua-ljsqlite3, synchronously, no subprocess involved.

Every method here calls Trapper:dismissablePopen(), which must run inside a
coroutine started by Trapper:wrap() to show progress/allow cancellation --
see ui/trapper.lua. If called unwrapped it still works, falling back to a
plain blocking io.popen() with no progress UI (Trapper's own fallback).
Callers should wrap the whole user action (call + resulting UI update) in a
single Trapper:wrap(), not just the call, since Trapper:wrap() returns as
soon as the wrapped function first yields rather than when it finishes.
]]

local Trapper = require("ui/trapper")
local json = require("json")
local lfs = require("libs/libkoreader-lfs")
local logger = require("logger")
local _ = require("aidoku_l10n")

local Runner = {}
Runner.__index = Runner

function Runner.new(bin_path)
    return setmetatable({ bin_path = bin_path }, Runner)
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

-- buildCommand assembles the shell command line: env assignments, an
-- optional low-priority prefix, the quoted binary and args, stderr
-- redirected to a capture file.
--
-- low_priority, if true and "nice" is available, runs under "nice -n 19"
-- so a background prefetch doesn't steal CPU from the foreground reader --
-- checked at runtime and skipped silently if absent, rather than risking
-- a missing-tool failure of the whole command.
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

-- errorMessage picks what to show the user out of a failed command's
-- stderr: aidoku-run's own "error: ..." line (the last one, prefix
-- stripped), not the "[download] page N/M" progress or guest "[print]"
-- lines that precede it; else the last non-empty line.
local function errorMessage(stderr)
    local last_error, last_line
    for line in stderr:gmatch("[^\n]+") do
        if line:match("%S") then
            last_line = line
            local msg = line:match("^error: (.*)")
            if msg then
                last_error = msg
            end
        end
    end
    return last_error or last_line
end

-- exec runs the binary with args, returning trimmed stdout on success or
-- nil plus an error message (see errorMessage, else a generic message) on
-- failure/cancellation. env, if given, is a table of
-- {VAR = value} exported into the command's environment (e.g.
-- FLARESOLVERR_HOST). See buildCommand() for low_priority.
function Runner:exec(args, progress_text, env, low_priority)
    -- One file per call (os.tmpname, under /tmp -- tmpfs, so no eMMC write
    -- churn): a background prefetch and a foreground call can overlap, and a
    -- shared file meant one's error popup could show the other's stderr.
    local stderr_path = os.tmpname()
    local cmd = buildCommand(self.bin_path, stderr_path, args, env, low_priority)

    local completed, output = Trapper:dismissablePopen(cmd, progress_text)
    local stderr = ""
    local f = io.open(stderr_path, "r")
    if f then
        stderr = f:read("*a") or ""
        f:close()
    end
    -- A cancelled call's process may still be running and writing here; on
    -- Linux, unlinking under it is harmless.
    os.remove(stderr_path)
    if not completed then
        return nil, _("Cancelled")
    end

    output = output and output:gsub("%s+$", "") or ""
    if output == "" then
        -- Full stderr goes to the log for debugging; only the error line is
        -- shown to the user.
        logger.warn("aidoku: command failed:", cmd, stderr)
        return nil, errorMessage(stderr) or _("command produced no output")
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
