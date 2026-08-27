--[[--
Store is the plugin's persistent state: bookmarked manga (the library),
read history, and small user preferences.

Preferences stay on KOReader's LuaSettings, one flat file at
DataStorage:getSettingsDir()/aidoku.lua -- cheap scalar values, no scale
concern. Library/read-history live in a separate SQLite file, <data_dir>/
library.db, opened directly via KOReader's bundled lua-ljsqlite3 FFI binding
(the same one downloadsengine.lua and other third-party KOReader plugins
like vocabbuilder.koplugin/db.lua use). They used to live on LuaSettings too,
but LuaSettings persists via pcall(dofile, file) -- a one-shot execution of a
serialized .lua file with zero chance of LuaJIT trace compilation (traces
only form from hot loops; a single dofile call never runs enough iterations)
-- so it scales linearly and stays fully interpreted as history grows,
unlike SQLite's indexed lookups. See CLAUDE.md.

Unlike downloadsengine.lua's index.db (schema owned by the Go side), this
file is the sole schema authority for library.db -- there's no Go process
guaranteed to run first. Every Store.new() call creates the schema
(idempotent, safe on every startup of either the FileManager-context or
ReaderUI-context plugin instance) and, once, migrates any pre-existing
LuaSettings library/history data across.

Library entries are keyed by source_key (the source's stable manifest ID,
e.g. "en.weebcentral") plus manga key, not by source_path -- a source
update changes its installed file's path (the "-v7.aix" -> "-v8.aix"
rename), which would otherwise silently orphan every bookmark pointing at
the old one. source_path is still stored per entry, but only as a
best-effort display/debugging hint; see installedsources.lua's
findByKey(), which librarybrowser.lua uses to re-resolve a bookmark's
current path from its key before opening it.
]]

local DataStorage = require("datastorage")
local Device = require("device")
local LuaSettings = require("luasettings")
local SQ3 = require("lua-ljsqlite3/init")
local util = require("util")

local Store = {}
Store.__index = Store

local LIBRARY_SCHEMA = [[
    CREATE TABLE IF NOT EXISTS library (
        source_key   TEXT NOT NULL,
        manga_key    TEXT NOT NULL,
        source_path  TEXT NOT NULL,
        title        TEXT NOT NULL,
        cover        TEXT,
        added_at     INTEGER NOT NULL,
        PRIMARY KEY (source_key, manga_key)
    );
    CREATE TABLE IF NOT EXISTS last_read (
        source_key    TEXT NOT NULL,
        manga_key     TEXT NOT NULL,
        last_read_at  INTEGER NOT NULL,
        PRIMARY KEY (source_key, manga_key)
    );
    CREATE TABLE IF NOT EXISTS read_chapters (
        source_key   TEXT NOT NULL,
        manga_key    TEXT NOT NULL,
        chapter_key  TEXT NOT NULL,
        PRIMARY KEY (source_key, manga_key, chapter_key)
    );
]]

-- migrateLegacyData is a one-time move of library/last_read/read_chapters
-- from the still-open LuaSettings instance into the SQLite tables above,
-- guarded by the library_migrated_to_sqlite marker so it only runs once.
--
-- Ordering matters for crash-safety: the SQLite transaction commits before
-- the LuaSettings keys are deleted/marker set. If the process dies in
-- between, the marker is never set, so this harmlessly re-runs (every
-- insert below is "OR IGNORE") on next startup.
--
-- Two plugin instances (FileManager-context, ReaderUI-context) can both
-- see the marker unset on first run after an update and both migrate --
-- safe, since both read the same pre-migration snapshot and insert
-- identical rows; whichever's marker-set-and-delete flush lands last wins,
-- and both are writing the same final content. This exact lost-update race
-- already exists today for any two concurrent LuaSettings writes from the
-- two instances, so it isn't newly introduced here.
local function migrateLegacyData(settings, db)
    if settings:readSetting("library_migrated_to_sqlite", false) then
        return
    end

    local library = settings:readSetting("library", {})
    local last_read = settings:readSetting("last_read", {})
    local read_chapters = settings:readSetting("read_chapters", {})

    db:exec("BEGIN")

    local lib_stmt = db:prepare([[
        INSERT OR IGNORE INTO library (source_key, manga_key, source_path, title, cover, added_at)
        VALUES (?, ?, ?, ?, ?, ?)
    ]])
    for _, entry in pairs(library) do
        -- Legacy pre-source_key bookmarks exist on real installs (see
        -- librarybrowser.lua's migrateLegacyBookmark) -- coerce a nil/absent
        -- source_key to "", not NULL, so the composite primary key still
        -- dedupes correctly (SQLite treats NULL as never-equal-to-itself
        -- even in PK columns).
        lib_stmt:bind(entry.source_key or "", entry.manga_key, entry.source_path or "",
            entry.title, entry.cover, entry.added_at or 0)
        lib_stmt:step()
        lib_stmt:reset()
    end
    lib_stmt:close()

    -- last_read/read_chapters' identity lives in the composite string key
    -- ("source_key|manga_key" / "source_key|manga_key|chapter_key"), not
    -- the value -- split on the first "|" each time, so the final part
    -- (manga_key, or chapter_key for the three-part case) absorbs any
    -- further "|" characters verbatim, since that's the field most likely
    -- to contain one in the wild. This unescaped-separator ambiguity
    -- already exists in the current libraryKey/lastReadKey/chapterReadKey
    -- format at rest -- this migration inherits it rather than introducing
    -- it, and can't retroactively resolve already-collided data, but no
    -- future write will ever need this splitting again afterward.
    local lr_stmt = db:prepare(
        "INSERT OR IGNORE INTO last_read (source_key, manga_key, last_read_at) VALUES (?, ?, ?)")
    for key, read_at in pairs(last_read) do
        local source_key, manga_key = key:match("^(.-)|(.*)$")
        if source_key and manga_key then
            lr_stmt:bind(source_key, manga_key, read_at)
            lr_stmt:step()
            lr_stmt:reset()
        end
    end
    lr_stmt:close()

    local rc_stmt = db:prepare(
        "INSERT OR IGNORE INTO read_chapters (source_key, manga_key, chapter_key) VALUES (?, ?, ?)")
    for key in pairs(read_chapters) do
        local source_key, rest = key:match("^(.-)|(.*)$")
        local manga_key, chapter_key
        if rest then
            manga_key, chapter_key = rest:match("^(.-)|(.*)$")
        end
        if source_key and manga_key and chapter_key then
            rc_stmt:bind(source_key, manga_key, chapter_key)
            rc_stmt:step()
            rc_stmt:reset()
        end
    end
    rc_stmt:close()

    db:exec("COMMIT")

    settings:delSetting("library")
    settings:delSetting("last_read")
    settings:delSetting("read_chapters")
    settings:saveSetting("library_migrated_to_sqlite", true)
    settings:flush()
end

-- Store.new(data_dir) -- data_dir is the plugin's own data directory (e.g.
-- main.lua's dataSubdir("")), library.db is created directly inside it.
function Store.new(data_dir)
    local settings = LuaSettings:open(DataStorage:getSettingsDir() .. "/aidoku.lua")

    data_dir = (data_dir or ""):gsub("/+$", "")
    util.makePath(data_dir)
    local db = SQ3.open(data_dir .. "/library.db")
    db:set_busy_timeout(5000)
    -- Real target hardware varies: some Kindle kernels can't safely mmap
    -- for WAL ("Kernel too old to support mmap'ed I/O on /mnt/us", per
    -- KOReader's own frontend/device/kindle/device.lua), so this idiom --
    -- copied from vocabbuilder.koplugin/db.lua's real, hardware-tested
    -- pattern, not invented here -- gates WAL behind KOReader's own
    -- device-capability check rather than forcing it unconditionally.
    if Device:canUseWAL() then
        db:exec("PRAGMA journal_mode=WAL;")
    else
        db:exec("PRAGMA journal_mode=TRUNCATE;")
    end
    db:exec(LIBRARY_SCHEMA)

    migrateLegacyData(settings, db)

    return setmetatable({ settings = settings, db = db }, Store)
end

-- ===== Library (bookmarked manga) =====

function Store:isBookmarked(source_key, manga_key)
    local stmt = self.db:prepare("SELECT 1 FROM library WHERE source_key=? AND manga_key=? LIMIT 1")
    stmt:bind(source_key, manga_key)
    local row = stmt:step()
    stmt:close()
    return row ~= nil
end

function Store:addBookmark(source_key, source_path, manga_key, title, cover)
    local stmt = self.db:prepare([[
        INSERT INTO library (source_key, manga_key, source_path, title, cover, added_at)
        VALUES (?, ?, ?, ?, ?, ?)
        ON CONFLICT (source_key, manga_key) DO UPDATE SET
            source_path=excluded.source_path, title=excluded.title,
            cover=excluded.cover, added_at=excluded.added_at
    ]])
    stmt:bind(source_key, manga_key, source_path, title, cover, os.time())
    stmt:step()
    stmt:close()
end

function Store:removeBookmark(source_key, manga_key)
    local stmt = self.db:prepare("DELETE FROM library WHERE source_key=? AND manga_key=?")
    stmt:bind(source_key, manga_key)
    stmt:step()
    stmt:close()
end

-- libraryEntries returns bookmarked manga as an array, oldest-added first,
-- with the same snake_case field names (entry.source_key, entry.title, ...)
-- librarybrowser.lua already reads directly.
function Store:libraryEntries()
    local stmt = self.db:prepare(
        "SELECT source_key, manga_key, source_path, title, cover, added_at FROM library ORDER BY added_at ASC")
    local entries = {}
    local row = {}
    while stmt:step(row) do
        table.insert(entries, {
            source_key = row[1],
            manga_key = row[2],
            source_path = row[3],
            title = row[4],
            cover = row[5],
            added_at = tonumber(row[6]) or 0,
        })
    end
    stmt:close()
    return entries
end

-- librarySortOrder is "name" (alphabetical, the default) or "last_read"
-- (most recently read chapter first, via lastReadAt below) -- see
-- librarybrowser.lua's hamburger menu, where it's toggled. This is a
-- preference, not library data -- stays on LuaSettings.
function Store:librarySortOrder()
    return self.settings:readSetting("library_sort_order", "name")
end

function Store:setLibrarySortOrder(order)
    self.settings:saveSetting("library_sort_order", order)
    self.settings:flush()
end

-- lastReadAt/markLastRead track os.time() of the last chapter opened for a
-- given manga, for the "last_read" library sort above. Recorded whenever a
-- chapter is opened (mangabrowser.lua's openLocal), regardless of whether
-- the manga is bookmarked -- entries for manga never added to the library
-- are simply never looked up.
function Store:lastReadAt(source_key, manga_key)
    local stmt = self.db:prepare("SELECT last_read_at FROM last_read WHERE source_key=? AND manga_key=?")
    stmt:bind(source_key, manga_key)
    local row = stmt:step()
    stmt:close()
    return (row and tonumber(row[1])) or 0
end

function Store:markLastRead(source_key, manga_key)
    local stmt = self.db:prepare([[
        INSERT INTO last_read (source_key, manga_key, last_read_at) VALUES (?, ?, ?)
        ON CONFLICT (source_key, manga_key) DO UPDATE SET last_read_at=excluded.last_read_at
    ]])
    stmt:bind(source_key, manga_key, os.time())
    stmt:step()
    stmt:close()
end

-- ===== Read chapters =====

-- Marked only automatically, when the reader hits a chapter's end-of-book
-- event (see main.lua's hookEndOfBook) -- there's no manual toggle, so row
-- presence alone means "read"; there's no boolean column to check.
function Store:isChapterRead(source_key, manga_key, chapter_key)
    local stmt = self.db:prepare(
        "SELECT 1 FROM read_chapters WHERE source_key=? AND manga_key=? AND chapter_key=? LIMIT 1")
    stmt:bind(source_key, manga_key, chapter_key)
    local row = stmt:step()
    stmt:close()
    return row ~= nil
end

function Store:markChapterRead(source_key, manga_key, chapter_key)
    local stmt = self.db:prepare(
        "INSERT OR IGNORE INTO read_chapters (source_key, manga_key, chapter_key) VALUES (?, ?, ?)")
    stmt:bind(source_key, manga_key, chapter_key)
    stmt:step()
    stmt:close()
end

-- ===== Preferences =====

-- sortOrder is "desc" (source's native order, usually newest-first) or
-- "asc" (reversed, oldest/first-chapter-first). Defaults to "desc".
function Store:sortOrder()
    return self.settings:readSetting("sort_order", "desc")
end

function Store:setSortOrder(order)
    self.settings:saveSetting("sort_order", order)
    self.settings:flush()
end

-- bufferChapters is how many upcoming chapters to prefetch when the user
-- starts reading one. 0 (the default) disables prefetching.
function Store:bufferChapters()
    return self.settings:readSetting("buffer_chapters", 0)
end

function Store:setBufferChapters(n)
    self.settings:saveSetting("buffer_chapters", n)
    self.settings:flush()
end

-- nextChapterMode is "off", "ask", or "auto" (the default), governing
-- whether reaching the end of a chapter offers/auto-advances to the next.
function Store:nextChapterMode()
    return self.settings:readSetting("next_chapter_mode", "ask")
end

function Store:setNextChapterMode(mode)
    self.settings:saveSetting("next_chapter_mode", mode)
    self.settings:flush()
end

-- flareSolverrHost is FLARESOLVERR_HOST's replacement for devices where
-- setting environment variables isn't practical (e.g. "localhost:8191").
-- "" (the default) means unset -- engine.lua injects this into every
-- source-running command's environment regardless, and an empty value
-- behaves identically to unset on the Go side (see
-- runtime/host/flaresolverr.go's FlareSolverrHostFromEnv, which
-- strings.TrimSpace()s it).
function Store:flareSolverrHost()
    return self.settings:readSetting("flaresolverr_host", "")
end

function Store:setFlareSolverrHost(host)
    self.settings:saveSetting("flaresolverr_host", host)
    self.settings:flush()
end

-- networkConcurrency bounds how many requests a source's net.send_all call
-- runs at once (see runtime/host/net.go's Net.MaxConcurrency). <= 0 (the
-- default) means "unset" -- engine.lua injects this into every
-- source-running command's environment regardless, and the Go side falls
-- back to its own default (8) when unset or non-positive.
function Store:networkConcurrency()
    return self.settings:readSetting("network_concurrency", 0)
end

function Store:setNetworkConcurrency(n)
    self.settings:saveSetting("network_concurrency", n)
    self.settings:flush()
end

-- downloadLimitBytes is the storage budget downloadsengine.lua prunes
-- against after each download. <= 0 (the default) means "no limit".
function Store:downloadLimitBytes()
    return self.settings:readSetting("download_limit_bytes", 0)
end

function Store:setDownloadLimitBytes(n)
    self.settings:saveSetting("download_limit_bytes", n)
    self.settings:flush()
end

-- repoURL is the index.min.json URL RepoBrowser installs sources from.
-- Stored value "" (the default) means the built-in Aidoku Community
-- Sources repo -- resolved here rather than left to callers, since
-- RepoBrowser always needs a real, non-empty URL to work with.
Store.DEFAULT_REPO_URL = "https://aidoku-community.github.io/sources/index.min.json"

function Store:repoURL()
    local url = self.settings:readSetting("repo_url", "")
    return url ~= "" and url or Store.DEFAULT_REPO_URL
end

-- Storing the literal default is normalized back to "" (unset), so a
-- future change to Store.DEFAULT_REPO_URL isn't shadowed by a
-- never-actually-customized value saved from an old default.
function Store:setRepoURL(url)
    url = url and url:gsub("^%s+", ""):gsub("%s+$", "") or ""
    if url == Store.DEFAULT_REPO_URL then
        url = ""
    end
    self.settings:saveSetting("repo_url", url)
    self.settings:flush()
end

return Store
