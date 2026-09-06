--[[--
Process-wide (require()-cached) in-memory cache of the "manga" command's
result (manga details + Chapters together, as Engine:mangaUpdate() returns
it), keyed by source_key then manga_key.

mangabrowser.lua's reload() and nextchapter.lua's handle() both call
Engine:mangaUpdate() whenever there's no already-downloaded next chapter --
a full subprocess round-trip into aidoku-run's source runtime and out to the
network. Reading through a manga chapter-by-chapter faster than prefetch
keeps up means that call re-runs on every single chapter transition, even
though the chapter list itself hasn't changed since the last fetch. Both
call sites check here first and only hit the network on a miss, storing the
result here on success -- see the note at each call site.

No file I/O and no TTL: this is deliberately just an in-memory table (like
openwidgets.lua/pendinglibrary.lua), shared between the FileManager and
ReaderUI plugin instances via require() caching (see CLAUDE.md's note that
those instances "don't share Lua state except through require()-cached
modules"). It only goes stale for the life of one such session and is wiped
by clear() -- see main.lua's Aidoku:onAidokuBrowseSources(), which calls
clear() on both a fresh Library open and the Reader->FileManager handoff, so
a manga's chapter list refreshes for real the next time the user goes back
to Library, while staying valid across chapter-to-chapter advances within
one read session.
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
