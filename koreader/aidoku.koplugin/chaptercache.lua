--[[--
Process-wide (require()-cached) in-memory cache of Engine:mangaUpdate()'s
result, keyed by source_key then manga_key -- avoids re-fetching the whole
chapter list on every chapter transition while reading faster than
prefetch keeps up.

No file I/O, no TTL: just an in-memory table, shared across plugin
instances via require() caching. Wiped wholesale by clear() (see main.lua's
onAidokuBrowseSources) on a fresh Library open or the Reader->FileManager
handoff -- stale for the rest of one session, valid across chapter
transitions within it.
]]

local cache = {}

local M = {}

function M.get(source_key, manga_key)
    local by_manga = cache[source_key]
    if not by_manga then
        return nil
    end
    return by_manga[manga_key]
end

function M.set(source_key, manga_key, updated)
    local by_manga = cache[source_key]
    if not by_manga then
        by_manga = {}
        cache[source_key] = by_manga
    end
    by_manga[manga_key] = updated
end

function M.clear()
    cache = {}
end

return M
