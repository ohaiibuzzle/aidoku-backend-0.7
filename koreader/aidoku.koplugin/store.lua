--[[--
Store is the plugin's persistent state: bookmarked manga (the library),
the downloaded-chapter index (which local file backs a given chapter, so we
don't re-download it), and small user preferences (chapter sort order).
Backed by KOReader's LuaSettings, one flat file at DataStorage:getSettingsDir().

Entries are keyed by source_path (the installed .aix file's path) plus
manga/chapter key, matching how source_path already flows through
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

local function downloadKey(source_path, manga_key, chapter_key)
    return source_path .. "|" .. manga_key .. "|" .. chapter_key
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

-- ===== Downloads (chapter -> local file index) =====

-- downloadedPath returns the local CBZ path for a chapter already
-- downloaded, or nil if it hasn't been.
function Store:downloadedPath(source_path, manga_key, chapter_key)
    local downloads = self.settings:readSetting("downloads", {})
    local entry = downloads[downloadKey(source_path, manga_key, chapter_key)]
    return entry and entry.path or nil
end

function Store:recordDownload(source_path, manga_key, chapter_key, path, manga_title, chapter_title)
    local downloads = self.settings:readSetting("downloads", {})
    downloads[downloadKey(source_path, manga_key, chapter_key)] = {
        source_path = source_path,
        manga_key = manga_key,
        chapter_key = chapter_key,
        path = path,
        manga_title = manga_title,
        chapter_title = chapter_title,
        downloaded_at = os.time(),
    }
    self.settings:flush()
end

-- removeDownload deletes both the index entry and the underlying file.
function Store:removeDownload(source_path, manga_key, chapter_key)
    local downloads = self.settings:readSetting("downloads", {})
    local key = downloadKey(source_path, manga_key, chapter_key)
    local entry = downloads[key]
    if not entry then
        return
    end
    os.remove(entry.path)
    downloads[key] = nil
    self.settings:flush()
end

-- allDownloads returns every downloaded chapter as an array, most recent
-- first.
function Store:allDownloads()
    local downloads = self.settings:readSetting("downloads", {})
    local entries = {}
    for _, entry in pairs(downloads) do
        table.insert(entries, entry)
    end
    table.sort(entries, function(a, b) return a.downloaded_at > b.downloaded_at end)
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

return Store
