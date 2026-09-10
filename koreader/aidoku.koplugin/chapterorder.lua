--[[--
"Reading order" helpers: chapters sorted by ascending ChapterNumber, the
canonical reading progression, independent of a manga screen's display
sort order (which is about how the list looks, not which chapter follows
which). Shared by mangabrowser.lua's prefetch and nextchapter.lua's
end-of-book auto-advance.
]]

local ChapterOrder = {}

-- aidoku-run's JSON encodes Go's nil *float32 ChapterNumber as JSON null,
-- which KOReader's json module decodes as a function sentinel rather than
-- Lua nil -- see the note on this in mangabrowser.lua.
function ChapterOrder.number(chapter)
    return type(chapter.ChapterNumber) == "number" and chapter.ChapterNumber or nil
end

-- orderedByReading returns all of chapters in ascending reading order.
-- Network chapter lists arrive newest-first, so this starts by reversing.
--
-- Not every chapter carries a ChapterNumber (e.g. en.weebcentral's
-- whole-volume bundle rows have only VolumeNumber, which isn't on the same
-- axis so can't substitute as a sort key). Numbered chapters are sorted
-- ascending among themselves, then re-threaded back into their original
-- reversed slots -- leaving unnumbered chapters pinned where the source
-- placed them, so ChapterOrder.after() can treat a bundle row as a normal
-- step instead of a gap.
function ChapterOrder.orderedByReading(chapters)
    local positional = {}
    for i = #chapters, 1, -1 do
        table.insert(positional, chapters[i])
    end

    local numbered = {}
    for _, c in ipairs(positional) do
        if ChapterOrder.number(c) then
            table.insert(numbered, c)
        end
    end
    table.sort(numbered, function(a, b) return a.ChapterNumber < b.ChapterNumber end)

    local ordered = {}
    local ni = 1
    for _, c in ipairs(positional) do
        if ChapterOrder.number(c) then
            table.insert(ordered, numbered[ni])
            ni = ni + 1
        else
            table.insert(ordered, c)
        end
    end
    return ordered
end

-- fromEntry adapts one downloads_engine row (lowerCamelCase JSON) into a
-- chapter-shaped table matching what orderedByReading/after/chapterLabel
-- expect from a network Chapters entry, so ordering works offline too.
-- Same null-as-function-sentinel guard as ChapterOrder.number above.
function ChapterOrder.fromEntry(entry)
    return {
        Key = entry.chapterKey,
        Title = entry.chapterTitle,
        ChapterNumber = type(entry.chapterNumber) == "number" and entry.chapterNumber or nil,
        VolumeNumber = type(entry.volumeNumber) == "number" and entry.volumeNumber or nil,
    }
end

-- fromEntries filters a downloads_engine:list() result to one manga and
-- adapts each row via fromEntry, for building an offline chapter list/
-- ordering when the network chapter list isn't available.
--
-- downloads_engine:list() entries arrive ordered by download recency, not
-- reading order, so this re-sorts by ChapterNumber descending (matching
-- the "newest first" convention a network list arrives in), pushing
-- unnumbered chapters to the end. table.sort isn't stable, so two
-- unnumbered chapters need an explicit tiebreaker (_order, their original
-- position) rather than relying on input order -- omitting it produced a
-- visibly scrambled list in practice.
function ChapterOrder.fromEntries(entries, source_key, manga_key)
    local chapters = {}
    for i, entry in ipairs(entries or {}) do
        if entry.sourceKey == source_key and entry.mangaKey == manga_key then
            local chapter = ChapterOrder.fromEntry(entry)
            chapter._order = i
            table.insert(chapters, chapter)
        end
    end
    table.sort(chapters, function(a, b)
        local na, nb = ChapterOrder.number(a), ChapterOrder.number(b)
        if na and nb then
            if na ~= nb then
                return na > nb
            end
        elseif na or nb then
            return na ~= nil
        end
        return a._order < b._order
    end)
    for _, chapter in ipairs(chapters) do
        chapter._order = nil
    end
    return chapters
end

-- after returns up to `count` chapters immediately following current_key in
-- reading order. Empty if current_key is last or unrecognized.
function ChapterOrder.after(chapters, current_key, count)
    local ordered = ChapterOrder.orderedByReading(chapters)
    local idx
    for i, c in ipairs(ordered) do
        if c.Key == current_key then
            idx = i
            break
        end
    end
    if not idx then
        return {}
    end
    local result = {}
    for i = idx + 1, math.min(idx + count, #ordered) do
        table.insert(result, ordered[i])
    end
    return result
end

return ChapterOrder
