--[[--
Store is the plugin's persistent state that stays cheap and simple as
plain Lua: bookmarked manga (the library) and small user preferences.
Backed by KOReader's LuaSettings, one flat file at
DataStorage:getSettingsDir(). The downloaded-chapter index used to live
here too, but moved to the Go backend (see downloadsengine.lua and the
downloads Go package) since it's queried on nearly every screen and a
SQLite index scales better than re-parsing a growing JSON blob on every
read.

Library entries are keyed by source_path (the installed .aix file's path)
plus manga key, matching how source_path already flows through
sourcesbrowser/searchbrowser/mangabrowser. Reinstalling a source at a new
version changes its filename and so its identity here -- same limitation
sourcesbrowser.lua already has for installed-source identity, not something
new to this store.
]]

local DataStorage = require("datastorage")
local LuaSettings = require("luasettings")

local Store = {}
Store.__index = Store

function Store.new()
    local settings = LuaSettings:open(DataStorage:getSettingsDir() .. "/aidoku.lua")
    return setmetatable({ settings = settings }, Store)
end

local function libraryKey(source_path, manga_key)
    return source_path .. "|" .. manga_key
end

-- ===== Library (bookmarked manga) =====

function Store:isBookmarked(source_path, manga_key)
    local library = self.settings:readSetting("library", {})
    return library[libraryKey(source_path, manga_key)] ~= nil
end

function Store:addBookmark(source_path, manga_key, title, cover)
    local library = self.settings:readSetting("library", {})
    library[libraryKey(source_path, manga_key)] = {
        source_path = source_path,
        manga_key = manga_key,
        title = title,
        cover = cover,
        added_at = os.time(),
    }
    self.settings:flush()
end

function Store:removeBookmark(source_path, manga_key)
    local library = self.settings:readSetting("library", {})
    library[libraryKey(source_path, manga_key)] = nil
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

return Store
