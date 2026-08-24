--[[--
DownloadsEngine wraps the bundled aidoku-downloads binary (see
cmd/aidoku-downloads/main.go and the downloads Go package): the SQLite-
backed index of which local CBZ file backs a given (source, manga, chapter),
replacing what used to be store.lua's own downloads table. Every field that
could be "absent" comes back as a zero value (empty string/array/0), never
JSON null, so callers don't need the null-decodes-as-a-function-sentinel
dance documented in mangabrowser.lua.

Deliberately no record() method: a download is indexed by aidoku-run's own
"download" command (via engine.lua's Engine:download, which takes a
downloads_dir argument) in the same process that writes the CBZ, not as a
separate step from here -- see the comment on this in
cmd/aidoku-downloads/main.go.

One exception to "every field comes back as a zero value, never JSON null":
an entry's chapterNumber/volumeNumber are nil-means-absent, same as the
network chapter list's ChapterNumber -- decode them via chapterorder.lua's
ChapterOrder.fromEntry, which already handles the
null-decodes-as-a-function-sentinel gotcha documented in mangabrowser.lua.

Calls run with an invisible progress widget (SQLite queries against a local
file are near-instant, not worth a visible spinner), but still go through
subprocess.lua's Runner/Trapper machinery -- see its docs for why that
matters even for a fast command. Every call below passes nil, not false, for
that widget's text argument -- see the note on this in engine.lua's
Engine:download() -- so a tap landing on the (invisible) widget during the
call still reaches whatever's underneath afterward instead of being
silently dropped.
]]

local Runner = require("subprocess")

local DownloadsEngine = {}
DownloadsEngine.__index = DownloadsEngine

function DownloadsEngine.new(bin_path, downloads_dir)
    return setmetatable({
        runner = Runner.new(bin_path, "aidoku-downloads-stderr.log"),
        downloads_dir = downloads_dir,
    }, DownloadsEngine)
end

function DownloadsEngine:isAvailable()
    return self.runner:isAvailable()
end

-- path returns the local CBZ path for a chapter, or "" if not downloaded.
-- source_key is the source's stable manifest ID (e.g. "en.weebcentral"),
-- not its installed file's path -- see the downloads Go package doc for
-- why entries are keyed by that instead.
function DownloadsEngine:path(source_key, manga_key, chapter_key)
    local result, err = self.runner:execJSON(
        { self.downloads_dir, "path", source_key, manga_key, chapter_key }, nil)
    if not result then
        return "", err
    end
    return result.path or ""
end

-- byPath is the reverse lookup: given a local file path (as opened in the
-- reader), find which (source, manga, chapter) it is. Returns nil if path
-- isn't indexed.
function DownloadsEngine:byPath(file_path)
    local result, err = self.runner:execJSON({ self.downloads_dir, "by-path", file_path }, nil)
    if not result then
        return nil, err
    end
    if not result.sourcePath or result.sourcePath == "" then
        return nil
    end
    return result
end

-- remove deletes both the index entry and its backing file (a no-op, not
-- an error, if it doesn't exist). source_key: see path() above.
function DownloadsEngine:remove(source_key, manga_key, chapter_key)
    return self.runner:exec({ self.downloads_dir, "remove", source_key, manga_key, chapter_key }, nil)
end

-- reassociate repoints every downloaded-chapter entry under old_source_key
-- to new_source_key -- a manual repair for entries a source rename/update
-- left orphaned (see the downloads Go package doc). Returns how many
-- entries were moved.
function DownloadsEngine:reassociate(old_source_key, new_source_key)
    local result, err = self.runner:execJSON(
        { self.downloads_dir, "reassociate", old_source_key, new_source_key }, nil)
    if not result then
        return 0, err
    end
    return result.reassociated or 0
end

-- list returns every downloaded chapter, most recently downloaded first.
function DownloadsEngine:list()
    local result, err = self.runner:execJSON({ self.downloads_dir, "list" }, nil)
    if not result then
        return nil, err
    end
    return result
end

-- totalBytes returns the sum of every indexed download's size.
function DownloadsEngine:totalBytes()
    local result, err = self.runner:execJSON({ self.downloads_dir, "total" }, nil)
    if not result then
        return 0, err
    end
    return result.totalBytes or 0
end

-- prune deletes the oldest downloads (index entry + file) until under
-- limit_bytes, returning what was removed. limit_bytes <= 0 means "no
-- limit" (store.lua's zero-value default for an unset preference).
function DownloadsEngine:prune(limit_bytes)
    if not limit_bytes or limit_bytes <= 0 then
        return {}
    end
    local result, err = self.runner:execJSON({ self.downloads_dir, "prune", tostring(math.floor(limit_bytes)) }, nil)
    if not result then
        return nil, err
    end
    return result
end

return DownloadsEngine
