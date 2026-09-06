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
-- Network chapter lists arrive newest-first by convention (see fromEntries
-- below), so this starts by reversing to oldest-first.
--
-- Not every chapter carries a ChapterNumber -- e.g. en.weebcentral lists
-- whole-volume bundle downloads ("Volume 2") as separate entries alongside
-- individually numbered chapters, with only a VolumeNumber set and no
-- ChapterNumber. VolumeNumber isn't on the same numeric axis as
-- ChapterNumber (a volume can bundle any range of chapters), so it can't be
-- used as a substitute sort key -- but the source's own list order already
-- places a bundle row where it chronologically belongs relative to the
-- surrounding numbered chapters. So: chapters with a ChapterNumber are
-- sorted ascending among themselves, then re-threaded back into their
-- original (reversed) positional slots, leaving every unnumbered chapter
-- pinned wherever the source placed it rather than dropped or shoved to one
-- end. This is what lets ChapterOrder.after() treat a Volume-bundle entry as
-- a normal step in the sequence instead of an invisible gap next-chapter/
-- prefetch can't cross.
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

-- fromEntry adapts one row from downloads_engine:list()/byPath() (lowerCamel-
-- Case JSON: chapterKey, chapterTitle, chapterNumber, volumeNumber) into a
-- chapter-shaped table (Key, Title, ChapterNumber, VolumeNumber) matching
-- what orderedByReading/after/chapterLabel expect from a network Chapters
-- entry -- so the same ordering logic works offline, against the downloads
-- index, not just against a freshly fetched chapter list. entry.chapterNumber
-- suffers the same null-decodes-as-a-function-sentinel gotcha as a network
-- chapter's ChapterNumber (see the note at the top of this file), hence the
-- same type(...) == "number" guard here.
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
-- downloads_engine:list() (and so the entries this is fed) is ordered by
-- download recency, not reading order -- if left as-is, a chapter
-- downloaded out of sequence (e.g. as a one-off before its neighbors) would
-- land wherever its download timestamp happens to put it, which looks
-- especially broken once mangabrowser.lua's asc/desc toggle reverses it (a
-- reversal only makes sense against a list already in reading order, same
-- as a network chapter list arrives in). So sort by ChapterNumber
-- descending here -- the same "newest chapter first" convention a network
-- chapter list normally arrives in -- with unnumbered chapters (no
-- ChapterNumber to place them by) pushed to the end. table.sort isn't
-- guaranteed stable, so two unnumbered chapters (which the comparator below
-- would otherwise treat as equal) can't just rely on keeping their input
-- order -- that produced a visibly scrambled list in practice. _order
-- (their position in the download-recency-ordered input, stripped again
-- before returning) breaks that tie explicitly, so unnumbered chapters end
-- up in a deterministic, still recency-ordered block instead.
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
