--[[--
Store is the plugin's persistent state that stays cheap and simple as
plain Lua: bookmarked manga (the library) and small user preferences.
Backed by KOReader's LuaSettings, one flat file at
DataStorage:getSettingsDir(). The downloaded-chapter index used to live
here too, but moved to the Go backend (see downloadsengine.lua and the
downloads Go package) since it's queried on nearly every screen and a
SQLite index scales better than re-parsing a growing JSON blob on every
read.

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
local LuaSettings = require("luasettings")

local Store = {}
Store.__index = Store

function Store.new()
    local settings = LuaSettings:open(DataStorage:getSettingsDir() .. "/aidoku.lua")
    return setmetatable({ settings = settings }, Store)
end

local function libraryKey(source_key, manga_key)
    return source_key .. "|" .. manga_key
end

-- ===== Library (bookmarked manga) =====

function Store:isBookmarked(source_key, manga_key)
    local library = self.settings:readSetting("library", {})
    return library[libraryKey(source_key, manga_key)] ~= nil
end

function Store:addBookmark(source_key, source_path, manga_key, title, cover)
    local library = self.settings:readSetting("library", {})
    library[libraryKey(source_key, manga_key)] = {
        source_key = source_key,
        source_path = source_path,
        manga_key = manga_key,
        title = title,
        cover = cover,
        added_at = os.time(),
    }
    self.settings:flush()
end

function Store:removeBookmark(source_key, manga_key)
    local library = self.settings:readSetting("library", {})
    library[libraryKey(source_key, manga_key)] = nil
    self.settings:flush()
end

-- libraryEntries returns bookmarked manga as an array, oldest-added first.
function Store:libraryEntries()
    local library = self.settings:readSetting("library", {})
    local entries = {}
    for _, entry in pairs(library) do
        table.insert(entries, entry)
    end
    table.sort(entries, function(a, b) return a.added_at < b.added_at end)
    return entries
end

-- ===== Read chapters =====

-- Marked only automatically, when the reader hits a chapter's end-of-book
-- event (see main.lua's hookEndOfBook) -- there's no manual toggle. Keyed
-- like library bookmarks (source_key + manga_key), plus chapter_key, so a
-- chapter's read state survives its download being pruned/removed.
local function chapterReadKey(source_key, manga_key, chapter_key)
    return source_key .. "|" .. manga_key .. "|" .. chapter_key
end

function Store:isChapterRead(source_key, manga_key, chapter_key)
    local read_chapters = self.settings:readSetting("read_chapters", {})
    return read_chapters[chapterReadKey(source_key, manga_key, chapter_key)] == true
end

function Store:markChapterRead(source_key, manga_key, chapter_key)
    local read_chapters = self.settings:readSetting("read_chapters", {})
    read_chapters[chapterReadKey(source_key, manga_key, chapter_key)] = true
    self.settings:saveSetting("read_chapters", read_chapters)
    self.settings:flush()
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
