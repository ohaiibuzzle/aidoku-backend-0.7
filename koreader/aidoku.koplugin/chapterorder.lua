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

-- orderedByReading returns the chapters that have a ChapterNumber, sorted
-- ascending. Chapters without one can't be reliably placed in a reading
-- sequence and are dropped -- next-chapter navigation isn't meaningful for
-- them anyway.
function ChapterOrder.orderedByReading(chapters)
    local ordered = {}
    for _, c in ipairs(chapters) do
        if ChapterOrder.number(c) then
            table.insert(ordered, c)
        end
    end
    table.sort(ordered, function(a, b) return a.ChapterNumber < b.ChapterNumber end)
    return ordered
end

-- after returns up to `count` chapters immediately following current_key in
-- reading order. Empty if current_key is last, unrecognized, or unnumbered.
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
