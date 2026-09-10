--[[--
DownloadsEngine reads/writes <downloads_dir>/index.db directly via
lua-ljsqlite3 -- no record() method here: a download is indexed by
aidoku-run's own "download" command, in the same process that writes the
CBZ, so a killed/failed download can never leave the index pointing at a
missing file. Go's downloads.Open() solely owns the schema; this file only
does reads/row writes and treats a missing `downloads` table as empty, not
an error (a fresh install may never have run aidoku-run yet). No WAL (some
Kindle kernels can't mmap for it), just a busy timeout on both sides for
lock contention. Numeric columns come back as Lua strings, not numbers --
always tonumber() them.
]]

local DocSettings = require("docsettings")
local SQ3 = require("lua-ljsqlite3/init")
local ffiUtil = require("ffi/util")
local lfs = require("libs/libkoreader-lfs")

local DownloadsEngine = {}
DownloadsEngine.__index = DownloadsEngine

local ENTRY_COLUMNS = "source_key, source_path, manga_key, chapter_key, path, "
    .. "manga_title, chapter_title, size_bytes, downloaded_at, chapter_number, volume_number"

-- rowToEntry decodes one ENTRY_COLUMNS-shaped row (as returned by
-- stmt:step()) into the lowerCamelCase-keyed table every caller expects.
local function rowToEntry(row)
    return {
        sourceKey = row[1],
        sourcePath = row[2],
        mangaKey = row[3],
        chapterKey = row[4],
        path = row[5],
        mangaTitle = row[6],
        chapterTitle = row[7],
        sizeBytes = tonumber(row[8]) or 0,
        downloadedAt = tonumber(row[9]) or 0,
        chapterNumber = tonumber(row[10]),
        volumeNumber = tonumber(row[11]),
    }
end

function DownloadsEngine.new(downloads_dir)
    local self = setmetatable({
        downloads_dir = downloads_dir,
        conn = nil,
    }, DownloadsEngine)
    local ok, conn = pcall(SQ3.open, downloads_dir .. "/index.db")
    if ok then
        conn:set_busy_timeout(5000)
        self.conn = conn
    end
    return self
end

-- isAvailable reports whether index.db could be opened at all (storage not
-- writable/mounted, etc). It says nothing about whether the `downloads`
-- table exists yet -- see tableExists().
function DownloadsEngine:isAvailable()
    return self.conn ~= nil
end

-- tableExists is the "has aidoku-run ever recorded a download" check --
-- sqlite_master always exists even in a brand-new/empty database, so this
-- never errors, unlike querying `downloads` directly on a fresh install.
function DownloadsEngine:tableExists()
    if not self.conn then
        return false
    end
    local count = self.conn:rowexec(
        "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='downloads'")
    return (tonumber(count) or 0) > 0
end

-- path returns the local CBZ path for a chapter, or "" if not downloaded.
-- source_key is the source's stable manifest ID (e.g. "en.weebcentral"),
-- not its installed file's path -- see the downloads Go package doc for
-- why entries are keyed by that instead.
function DownloadsEngine:path(source_key, manga_key, chapter_key)
    if not self:tableExists() then
        return ""
    end
    local stmt = self.conn:prepare(
        "SELECT path FROM downloads WHERE source_key=? AND manga_key=? AND chapter_key=?")
    stmt:bind(source_key, manga_key, chapter_key)
    local row = stmt:step()
    stmt:close()
    return row and row[1] or ""
end

-- byPath is the reverse lookup: given a local file path (as opened in the
-- reader), find which (source, manga, chapter) it is. Returns nil if path
-- isn't indexed.
function DownloadsEngine:byPath(file_path)
    if not self:tableExists() then
        return nil
    end
    local stmt = self.conn:prepare("SELECT " .. ENTRY_COLUMNS .. " FROM downloads WHERE path=?")
    stmt:bind(file_path)
    local row = stmt:step()
    stmt:close()
    if not row then
        return nil
    end
    return rowToEntry(row)
end

-- remove deletes the index entry, its backing file, and its KOReader
-- reading-progress sidecar (all no-ops, not errors, if already absent).
-- Sidecar cleanup is unconditional (not just Ephemeral Mode), using
-- DocSettings:getSidecarDir rather than a guessed "<path>.sdr" path since
-- the user's document_metadata_folder setting can relocate sidecars away
-- from the document. Best-effort: a purgeDir failure doesn't fail
-- remove() itself.
function DownloadsEngine:remove(source_key, manga_key, chapter_key)
    local path = self:path(source_key, manga_key, chapter_key)
    if path == "" then
        return true
    end
    local stmt = self.conn:prepare(
        "DELETE FROM downloads WHERE source_key=? AND manga_key=? AND chapter_key=?")
    stmt:bind(source_key, manga_key, chapter_key)
    stmt:step()
    stmt:close()
    if lfs.attributes(path, "mode") == "file" then
        local ok, err = os.remove(path)
        if not ok then
            return false, err
        end
    end
    local sidecar_dir = DocSettings:getSidecarDir(path)
    if sidecar_dir ~= "" and lfs.attributes(sidecar_dir, "mode") == "directory" then
        ffiUtil.purgeDir(sidecar_dir)
    end
    return true
end

-- list returns every downloaded chapter, most recently downloaded first.
function DownloadsEngine:list()
    if not self:tableExists() then
        return {}
    end
    local stmt = self.conn:prepare(
        "SELECT " .. ENTRY_COLUMNS .. " FROM downloads ORDER BY downloaded_at DESC")
    local entries = {}
    local row = {}
    while stmt:step(row) do
        table.insert(entries, rowToEntry(row))
    end
    stmt:close()
    return entries
end

-- totalBytes returns the sum of every indexed download's size.
function DownloadsEngine:totalBytes()
    if not self:tableExists() then
        return 0
    end
    return tonumber(self.conn:rowexec("SELECT SUM(size_bytes) FROM downloads")) or 0
end

-- prune deletes the oldest downloads (index entry + file) until under
-- limit_bytes, returning what was removed. limit_bytes <= 0 means "no
-- limit" (store.lua's zero-value default for an unset preference).
function DownloadsEngine:prune(limit_bytes)
    if not limit_bytes or limit_bytes <= 0 then
        return {}
    end
    local total = self:totalBytes()
    if total <= limit_bytes then
        return {}
    end
    local entries = self:list() -- most-recently-downloaded first
    local removed = {}
    for i = #entries, 1, -1 do -- walk from the oldest end
        if total <= limit_bytes then
            break
        end
        local e = entries[i]
        local ok = self:remove(e.sourceKey, e.mangaKey, e.chapterKey)
        if ok then
            total = total - e.sizeBytes
            table.insert(removed, e)
        end
    end
    return removed
end

-- removeAllExcept deletes every indexed download except the one at keep_path
-- (index entry + file), or everything if keep_path is nil. Used by Ephemeral
-- Mode: to purge the RAM-disk directory when the mode is turned off (keep_path
-- = nil), and to drop the previous chapter right after a new one opens
-- (keep_path = the newly opened path) -- see mangabrowser.lua/nextchapter.lua/
-- settingsbrowser.lua.
function DownloadsEngine:removeAllExcept(keep_path)
    for _, e in ipairs(self:list()) do
        if e.path ~= keep_path then
            self:remove(e.sourceKey, e.mangaKey, e.chapterKey)
        end
    end
end

-- reassociate repoints every downloaded-chapter entry under old_source_key
-- to new_source_key -- a manual repair for entries a source rename/update
-- left orphaned (see the downloads Go package doc). Returns how many
-- entries were moved.
function DownloadsEngine:reassociate(old_source_key, new_source_key)
    if not self:tableExists() then
        return 0
    end
    -- If new_source_key already has an entry for some (manga, chapter) also
    -- present under old_source_key, drop the old_source_key row instead of
    -- overwriting the existing one (presumably the more recently
    -- verified-working one) -- same rule as Store.Reassociate in Go.
    local del_stmt = self.conn:prepare([[
        DELETE FROM downloads
        WHERE source_key = ?
        AND EXISTS (
            SELECT 1 FROM downloads AS d2
            WHERE d2.source_key = ? AND d2.manga_key = downloads.manga_key AND d2.chapter_key = downloads.chapter_key
        )
    ]])
    del_stmt:bind(old_source_key, new_source_key)
    del_stmt:step()
    del_stmt:close()

    -- lua-ljsqlite3 doesn't expose sqlite3_changes, so count the rows the
    -- UPDATE below is about to touch first, immediately before running it.
    local count_stmt = self.conn:prepare("SELECT count(*) FROM downloads WHERE source_key=?")
    count_stmt:bind(old_source_key)
    local row = count_stmt:step()
    count_stmt:close()
    local n = (row and tonumber(row[1])) or 0

    local upd_stmt = self.conn:prepare("UPDATE downloads SET source_key = ? WHERE source_key = ?")
    upd_stmt:bind(new_source_key, old_source_key)
    upd_stmt:step()
    upd_stmt:close()

    return n
end

return DownloadsEngine
