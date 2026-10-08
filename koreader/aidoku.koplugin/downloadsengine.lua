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
local _ = require("aidoku_l10n")

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

-- purgeSidecar deletes a document's KOReader reading-progress sidecar,
-- using DocSettings:getSidecarDir rather than a guessed "<path>.sdr" path
-- since the user's document_metadata_folder setting can relocate sidecars
-- away from the document. Best-effort: a purgeDir failure is ignored.
local function purgeSidecar(path)
    local sidecar_dir = DocSettings:getSidecarDir(path)
    if sidecar_dir ~= "" and lfs.attributes(sidecar_dir, "mode") == "directory" then
        ffiUtil.purgeDir(sidecar_dir)
    end
end

-- query prepares sql, binds binds (if any), and returns fn(stmt) -- or
-- fallback instead of raising if any step fails (a busy timeout running
-- out under a concurrent aidoku-run write, a damaged index.db after power
-- loss, ...). Every caller is a UI screen that doesn't expect an error, so
-- an unguarded prepare/step would crash it. The statement is always closed.
local function query(conn, fallback, sql, binds, fn)
    local ok, stmt = pcall(conn.prepare, conn, sql)
    if not ok then
        return fallback
    end
    local ok_run, result = pcall(function()
        if binds then
            stmt:bind(unpack(binds))
        end
        return fn(stmt)
    end)
    pcall(stmt.close, stmt)
    if not ok_run then
        return fallback
    end
    return result
end

local function firstRow(stmt)
    return stmt:step()
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
-- sqlite_master always exists even in a brand-new/empty database, unlike
-- querying `downloads` directly on a fresh install. An unreadable database
-- counts as "no table", so every caller degrades to its empty result.
function DownloadsEngine:tableExists()
    if not self.conn then
        return false
    end
    local ok, count = pcall(self.conn.rowexec, self.conn,
        "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='downloads'")
    return ok and (tonumber(count) or 0) > 0
end

-- indexedPath returns the path recorded for a chapter, or "" if there's no
-- row -- whether or not that file still exists. Only remove() wants this;
-- everything else goes through path().
function DownloadsEngine:indexedPath(source_key, manga_key, chapter_key)
    if not self:tableExists() then
        return ""
    end
    local row = query(self.conn, nil,
        "SELECT path FROM downloads WHERE source_key=? AND manga_key=? AND chapter_key=?",
        { source_key, manga_key, chapter_key }, firstRow)
    return row and row[1] or ""
end

-- path returns the local CBZ path for a chapter, or "" if not downloaded --
-- including a stale row whose file is gone (deleted from KOReader's file
-- browser, or left behind by aidoku-run's same-filename disambiguation), so
-- no caller ever tries to open a missing file. source_key is the source's
-- stable manifest ID (e.g. "en.weebcentral"), not its installed file's
-- path -- see the downloads Go package doc for why entries are keyed by that.
function DownloadsEngine:path(source_key, manga_key, chapter_key)
    local p = self:indexedPath(source_key, manga_key, chapter_key)
    if p ~= "" and lfs.attributes(p, "mode") ~= "file" then
        return ""
    end
    return p
end

-- byPath is the reverse lookup: given a local file path (as opened in the
-- reader), find which (source, manga, chapter) it is. Returns nil if path
-- isn't indexed.
function DownloadsEngine:byPath(file_path)
    if not self:tableExists() then
        return nil
    end
    local row = query(self.conn, nil,
        "SELECT " .. ENTRY_COLUMNS .. " FROM downloads WHERE path=?", { file_path }, firstRow)
    if not row then
        return nil
    end
    return rowToEntry(row)
end

-- remove deletes the index entry, its backing file, and its KOReader
-- reading-progress sidecar (all no-ops, not errors, if already absent).
-- Sidecar cleanup is unconditional (not just Ephemeral Mode) -- see
-- purgeSidecar.
function DownloadsEngine:remove(source_key, manga_key, chapter_key)
    local path = self:indexedPath(source_key, manga_key, chapter_key)
    if path == "" then
        return true
    end
    -- File first: if that fails the row stays, so a later remove/prune can
    -- retry, instead of leaving a file nothing indexes (and so nothing ever
    -- deletes).
    if lfs.attributes(path, "mode") == "file" then
        local ok, err = os.remove(path)
        if not ok then
            return false, err
        end
    end
    purgeSidecar(path)
    local deleted = query(self.conn, false,
        "DELETE FROM downloads WHERE source_key=? AND manga_key=? AND chapter_key=?",
        { source_key, manga_key, chapter_key }, function(stmt) stmt:step() return true end)
    if not deleted then
        return false
    end
    return true
end

-- resetProgress drops the reading-progress sidecar of every downloaded
-- chapter of one manga, leaving the CBZs themselves in place, so they
-- reopen at page 1. Used by mangabrowser.lua's "Reset reading progress".
function DownloadsEngine:resetProgress(source_key, manga_key)
    if not self:tableExists() then
        return
    end
    for _idx, e in ipairs(self:list() or {}) do
        if e.sourceKey == source_key and e.mangaKey == manga_key then
            purgeSidecar(e.path)
        end
    end
end

-- list returns every downloaded chapter, most recently downloaded first, or
-- (nil, err) if the index couldn't be read -- what downloadsbrowser.lua's
-- caller checks for.
function DownloadsEngine:list()
    if not self:tableExists() then
        return {}
    end
    local entries = query(self.conn, nil,
        "SELECT " .. ENTRY_COLUMNS .. " FROM downloads ORDER BY downloaded_at DESC", nil,
        function(stmt)
            local out, row = {}, {}
            while stmt:step(row) do
                table.insert(out, rowToEntry(row))
            end
            return out
        end)
    if not entries then
        return nil, _("Could not read the downloads index.")
    end
    return entries
end

-- totalBytes returns the sum of every indexed download's size.
function DownloadsEngine:totalBytes()
    if not self:tableExists() then
        return 0
    end
    local ok, total = pcall(self.conn.rowexec, self.conn, "SELECT SUM(size_bytes) FROM downloads")
    return ok and tonumber(total) or 0
end

-- prune deletes the oldest downloads (index entry + file) until under
-- limit_bytes, returning what was removed. limit_bytes <= 0 means "no
-- limit" (store.lua's zero-value default for an unset preference).
--
-- keep_path, if given, is never deleted -- callers pass the chapter
-- they're about to open, so a limit smaller than that chapter's own size
-- can't delete it out from under them (confirmed on-device: prefetch.lua
-- once called this with no keep_path at all and deleted the chapter the
-- user was actively reading).
function DownloadsEngine:prune(limit_bytes, keep_path)
    if not limit_bytes or limit_bytes <= 0 then
        return {}
    end
    local total = self:totalBytes()
    if total <= limit_bytes then
        return {}
    end
    local entries = self:list() or {} -- most-recently-downloaded first
    local removed = {}
    for i = #entries, 1, -1 do -- walk from the oldest end
        if total <= limit_bytes then
            break
        end
        local e = entries[i]
        if e.path ~= keep_path then
            local ok = self:remove(e.sourceKey, e.mangaKey, e.chapterKey)
            if ok then
                total = total - e.sizeBytes
                table.insert(removed, e)
            end
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
    for _idx, e in ipairs(self:list() or {}) do
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
    local function step(stmt) stmt:step() return true end
    if not query(self.conn, false, [[
        DELETE FROM downloads
        WHERE source_key = ?
        AND EXISTS (
            SELECT 1 FROM downloads AS d2
            WHERE d2.source_key = ? AND d2.manga_key = downloads.manga_key AND d2.chapter_key = downloads.chapter_key
        )
    ]], { old_source_key, new_source_key }, step) then
        return 0
    end

    -- lua-ljsqlite3 doesn't expose sqlite3_changes, so count the rows the
    -- UPDATE below is about to touch first, immediately before running it.
    local row = query(self.conn, nil, "SELECT count(*) FROM downloads WHERE source_key=?",
        { old_source_key }, firstRow)
    local n = (row and tonumber(row[1])) or 0

    if not query(self.conn, false, "UPDATE downloads SET source_key = ? WHERE source_key = ?",
        { new_source_key, old_source_key }, step) then
        return 0
    end
    return n
end

return DownloadsEngine
