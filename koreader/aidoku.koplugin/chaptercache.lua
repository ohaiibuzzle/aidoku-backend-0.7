--[[--
Process-wide (require()-cached) in-memory cache of Engine:mangaUpdate()'s
result for one manga (single slot) -- avoids re-fetching the whole
chapter list on every chapter transition while reading faster than
prefetch keeps up.

No file I/O, no TTL: just an in-memory table, shared across plugin
instances via require() caching. Wiped wholesale by clear() (see main.lua's
onAidokuBrowseSources) on a fresh Library open or the Reader->FileManager
handoff -- stale for the rest of one session, valid across chapter
transitions within it.
]]

-- Single slot: only the manga being read needs it, and a full chapter list
-- can be large on a 256MB device, so set() replaces rather than accumulates.
local cache = {}

local M = {}

function M.get(source_key, manga_key)
    if cache.source_key == source_key and cache.manga_key == manga_key then
        return cache.updated
    end
    return nil
end

function M.set(source_key, manga_key, updated)
    cache = { source_key = source_key, manga_key = manga_key, updated = updated }
end

function M.clear()
    cache = {}
end

return M
