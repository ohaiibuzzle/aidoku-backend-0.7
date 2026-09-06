--[[--
Primary Aidoku screen: just the user's bookmarked manga. The title bar's
left "hamburger" button opens a small menu of Settings (source
installs/downloads/preferences, see settingsbrowser.lua) and Search all
sources (see globalsearchbrowser.lua), keeping this list itself purely
the library. Tapping an entry opens MangaBrowser directly against its
cached key -- no re-search needed.

Each bookmark stores source_key (stable across a source update) rather
than trusting its stored source_path (which a source update can change --
see installedsources.lua and the downloads Go package doc), so opening one
resolves the current path from the key first via findByKey.
]]

local ButtonDialog = require("ui/widget/buttondialog")
local ConfirmBox = require("ui/widget/confirmbox")
local InfoMessage = require("ui/widget/infomessage")
local InstalledSources = require("installedsources")
local Menu = require("ui/widget/menu")
local OpenWidgets = require("openwidgets")
local Trapper = require("ui/trapper")
local UIManager = require("ui/uimanager")
local T = require("ffi/util").template
local _ = require("gettext")

local LibraryBrowser = Menu:extend{
    title = _("Library"),
    title_bar_left_icon = "appbar.menu",
}

function LibraryBrowser:init()
    self.subtitle = self.store:isEphemeralMode() and _("Ephemeral Mode") or ""
    self.item_table = self:genItemTable()
    Menu.init(self)
    OpenWidgets.push(self)
end

-- Every top-level Aidoku screen (see openwidgets.lua) unregisters itself
-- here so main.lua's hookClose can close any that are still open before
-- FileManager/ReaderUI itself closes -- otherwise a screen left open when
-- KOReader exits stays on UIManager's window stack forever, silently
-- blocking KOReader from actually quitting.
function LibraryBrowser:onCloseWidget()
    Menu.onCloseWidget(self)
    OpenWidgets.remove(self)
end

-- entryKey resolves a library entry's identity for lastReadAt lookups --
-- same source_key-with-source_path-fallback as onMenuHold's removeBookmark
-- key, needed because a never-opened legacy bookmark (see
-- migrateLegacyBookmark) has no source_key yet.
local function entryKey(entry)
    return (entry.source_key and entry.source_key ~= "") and entry.source_key or entry.source_path
end

function LibraryBrowser:sortedLibraryEntries()
    local entries = self.store:libraryEntries()
    local order = self.store:librarySortOrder()
    if order == "last_read" then
        table.sort(entries, function(a, b)
            return self.store:lastReadAt(entryKey(a), a.manga_key) > self.store:lastReadAt(entryKey(b), b.manga_key)
        end)
    else
        table.sort(entries, function(a, b) return a.title:lower() < b.title:lower() end)
    end
    return entries
end

function LibraryBrowser:genItemTable()
    local item_table = {}
    for _, entry in ipairs(self:sortedLibraryEntries()) do
        table.insert(item_table, { text = entry.title, library_entry = entry })
    end
    if #item_table == 0 then
        table.insert(item_table, {
            text = _("Your library is empty. Open the menu (top left) to search or browse sources."),
            dim = true,
            is_placeholder = true,
        })
    end
    return item_table
end

function LibraryBrowser:refresh()
    self.item_table = self:genItemTable()
    self:updateItems()
    -- Picks up an Ephemeral Mode toggle from Settings immediately (via its
    -- refresh_callback below), without needing this whole screen rebuilt.
    if self.title_bar then
        self.title_bar:setSubTitle(self.store:isEphemeralMode() and _("Ephemeral Mode") or "", true)
    end
end

-- The hamburger menu (see title_bar_left_icon above) groups the two
-- screens that used to be separate title bar icons/list rows: Settings
-- and Search all sources. Anchored under the hamburger icon itself
-- (same technique as FileManager's own "+" dropdown -- see
-- apps/filemanager/filemanager.lua's plus_dialog) rather than centered,
-- so it reads as a small in-place menu instead of a full popup.
function LibraryBrowser:onLeftButtonTap()
    local dialog
    dialog = ButtonDialog:new{
        shrink_unneeded_width = true,
        anchor = function() return self.title_bar.left_button.image.dimen end,
        buttons = {
            {{
                text = _("Settings"),
                callback = function()
                    UIManager:close(dialog)
                    local SettingsBrowser = require("settingsbrowser")
                    UIManager:show(SettingsBrowser:new{
                        aidoku = self.aidoku,
                        engine = self.engine,
                        store = self.store,
                        downloads_engine = self.downloads_engine,
                        ui = self.ui,
                        sources_dir = self.sources_dir,
                        downloads_dir = self.downloads_dir,
                        is_popout = false,
                        is_borderless = true,
                        title_bar_fm_style = true,
                        refresh_callback = function()
                            -- Toggling Ephemeral Mode in Settings calls
                            -- self.aidoku:refreshDownloadsDir() immediately --
                            -- resync our own copies so the next manga opened
                            -- from this Library screen downloads to the right
                            -- place, not the one cached at our own :init().
                            self.downloads_dir = self.aidoku.downloads_dir
                            self.downloads_engine = self.aidoku.downloads_engine
                            self:refresh()
                        end,
                    })
                end,
            }},
            {{
                text = _("Search all sources"),
                callback = function()
                    UIManager:close(dialog)
                    local GlobalSearchBrowser = require("globalsearchbrowser")
                    UIManager:show(GlobalSearchBrowser:new{
                        engine = self.engine,
                        store = self.store,
                        downloads_engine = self.downloads_engine,
                        ui = self.ui,
                        sources_dir = self.sources_dir,
                        downloads_dir = self.downloads_dir,
                        is_popout = false,
                        is_borderless = true,
                        title_bar_fm_style = true,
                        refresh_callback = function() self:refresh() end,
                    })
                end,
            }},
            {{
                text = self.store:librarySortOrder() == "last_read" and _("Sort: Last read") or _("Sort: Name"),
                callback = function()
                    UIManager:close(dialog)
                    local next_order = self.store:librarySortOrder() == "last_read" and "name" or "last_read"
                    self.store:setLibrarySortOrder(next_order)
                    self:refresh()
                end,
            }},
        },
    }
    UIManager:show(dialog)
end

-- migrateLegacyBookmark handles a bookmark saved before source_key existed
-- (its stored key is nil/empty, only source_path is set): read that
-- source's manifest id directly (like installedsources.lua does for any
-- other source) and re-save the bookmark under it. Returns the entry's
-- (possibly newly-set) source_key, or nil if source_path is gone too (a
-- pre-existing bookmark this can't recover -- see the module doc).
function LibraryBrowser:migrateLegacyBookmark(entry)
    if entry.source_key and entry.source_key ~= "" then
        return entry.source_key
    end
    if not entry.source_path or entry.source_path == "" then
        return nil
    end
    local manifest = self.engine:manifest(entry.source_path)
    if not manifest or type(manifest.key) ~= "string" or manifest.key == "" then
        return nil
    end
    -- The pre-source_key libraryKey() format was source_path .. "|" ..
    -- manga_key -- passing the old source_path in as if it were a
    -- source_key here reconstructs that exact same string, so this removes
    -- the legacy entry correctly regardless of the key format change.
    self.store:removeBookmark(entry.source_path, entry.manga_key)
    self.store:addBookmark(manifest.key, entry.source_path, entry.manga_key, entry.title, entry.cover)
    return manifest.key
end

function LibraryBrowser:onMenuSelect(item)
    if item.is_placeholder then
        return true
    end
    local entry = item.library_entry
    -- entry.source_path is only a stale hint (see the module doc) --
    -- resolve the current installed path from its key before opening.
    -- Both this and migrateLegacyBookmark are subprocess calls (see
    -- installedsources.lua/engine:manifest), hence the wrap.
    Trapper:wrap(function()
        local source_key = self:migrateLegacyBookmark(entry)
        local found = source_key and InstalledSources.findByKey(self.sources_dir, self.engine, source_key)
        if not found then
            UIManager:show(InfoMessage:new{
                text = T(_("'%1' is no longer installed. Hold this entry to remove it from your library."), entry.title),
            })
            return
        end
        local MangaBrowser = require("mangabrowser")
        UIManager:show(MangaBrowser:new{
            engine = self.engine,
            store = self.store,
            downloads_engine = self.downloads_engine,
            ui = self.ui,
            source_path = found.path,
            source_key = source_key,
            source_name = found.name,
            downloads_dir = self.downloads_dir,
            manga = { Key = entry.manga_key, Title = entry.title },
            is_popout = false,
            is_borderless = true,
            title_bar_fm_style = true,
        })
    end)
    return true
end

function LibraryBrowser:onMenuHold(item)
    if item.is_placeholder then
        return true
    end
    local entry = item.library_entry
    UIManager:show(ConfirmBox:new{
        text = T(_("Remove '%1' from your library?"), entry.title),
        ok_text = _("Remove"),
        ok_callback = function()
            -- A never-opened legacy bookmark (see migrateLegacyBookmark)
            -- has no source_key yet -- its removeBookmark key is still
            -- reconstructible from source_path (see the comment on this in
            -- migrateLegacyBookmark).
            local key = (entry.source_key and entry.source_key ~= "") and entry.source_key or entry.source_path
            self.store:removeBookmark(key, entry.manga_key)
            self:refresh()
        end,
    })
    return true
end

return LibraryBrowser
