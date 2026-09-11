--[[--
Shared "silently download N upcoming chapters" prefetch logic, so the
buffer stays primed regardless of how the user is currently advancing --
opening a chapter from mangabrowser.lua's list, or auto-advancing through
nextchapter.lua's end-of-book handler. Runs via Trapper with a false
progress widget (see the note on this in engine.lua's download()), so it
doesn't interrupt whatever the user is doing with a visible dialog.
]]

local ChapterOrder = require("chapterorder")
local Trapper = require("ui/trapper")
local util = require("util")
local T = require("ffi/util").template
local _ = require("gettext")

local Prefetch = {}

-- Manga/Chapter Title fields are plain (non-pointer) Go strings, so an
-- absent title decodes as "" rather than null -- and "" is truthy in Lua,
-- so an explicit emptiness check is needed, not just `x or fallback`.
function Prefetch.mangaLabel(manga)
    if type(manga.Title) == "string" and manga.Title ~= "" then
        return manga.Title
    end
    return manga.Key
end

function Prefetch.chapterLabel(chapter)
    if type(chapter.Title) == "string" and chapter.Title ~= "" then
        return chapter.Title
    end
    if ChapterOrder.number(chapter) then
        return T(_("Chapter %1"), chapter.ChapterNumber)
    end
    return chapter.Key
end

-- ahead silently downloads up to ctx.store:effectiveBufferChapters() upcoming
-- chapters (reading order, not display sort) after chapter_key, so they're
-- likely already local by the time the reader reaches them.
--
-- Two callers racing the same not-yet-downloaded chapter (e.g. this
-- prefetch still running when the reader reaches it via nextchapter.lua's
-- network fallback) is deduplicated Go-side via aidoku-run's cross-process
-- flock, not here: a Lua-side lock can't tell "the subprocess call
-- returned" apart from "the download actually finished" (KOReader's
-- Trapper can report a download cancelled mid-write), so it isn't reliable.
--
-- ctx fields: engine, downloads_engine, store, downloads_dir, source_path,
-- source_key, manga (the full manga table, for its Key/Title).
--
-- on_downloaded(chapter, path), if given, is called per chapter newly
-- downloaded this pass. on_complete(any_new), if given, is called once
-- after the pass finishes.
function Prefetch.ahead(ctx, chapters, chapter_key, on_downloaded, on_complete)
    local n = ctx.store:effectiveBufferChapters()
    if n <= 0 then
        return
    end
    local upcoming = ChapterOrder.after(chapters, chapter_key, n)
    if #upcoming == 0 then
        return
    end
    Trapper:wrap(function()
        local any_new = false
        for _, c in ipairs(upcoming) do
            if ctx.downloads_engine:path(ctx.source_key, ctx.manga.Key, c.Key) == "" then
                local chapter_filename = util.getSafeFilename(
                    Prefetch.chapterLabel(c) .. ".cbz", ctx.downloads_dir)
                local out_path = ctx.downloads_dir .. "/" .. chapter_filename
                local manga_dir_name = Prefetch.mangaLabel(ctx.manga) .. " [" .. ctx.source_key .. "]"
                local path = ctx.engine:download(
                    ctx.source_path, ctx.manga.Key, c.Key, out_path, ctx.downloads_dir, true, nil, manga_dir_name)
                if path then
                    any_new = true
                    if on_downloaded then
                        on_downloaded(c, path)
                    end
                end
            end
        end
        -- The storage-limit prune is a persistent-storage concept -- under
        -- Ephemeral Mode, the caller's own removeAllExcept() already keeps
        -- at most ~1-2 chapters resident, and running prune() on top of
        -- that risks deleting a chapter this same call just downloaded
        -- (see mangabrowser.lua's downloadAndOpen for the same guard).
        --
        -- chapter_key is the chapter the caller just opened (downloadAndOpen/
        -- fetchAndOpen kick this off right after opening it, deferred to the
        -- next tick) -- it's still being read when this runs, so its path is
        -- passed as keep_path the same way those callers protect it from
        -- their own prune() calls. Without this, a storage limit small
        -- enough to need pruning at all could delete the chapter the user
        -- is actively reading right out from under them (confirmed
        -- on-device: this prune call has no keep_path of its own, so it
        -- deleted the just-opened chapter's file even though downloadAndOpen's
        -- own prune, moments earlier, had correctly protected it).
        if any_new and not ctx.store:isEphemeralMode() then
            local keep_path = ctx.downloads_engine:path(ctx.source_key, ctx.manga.Key, chapter_key)
            ctx.downloads_engine:prune(ctx.store:downloadLimitBytes(), keep_path)
        end
        if on_complete then
            on_complete(any_new)
        end
    end)
end

return Prefetch
