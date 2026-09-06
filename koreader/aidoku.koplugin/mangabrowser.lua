--[[--
Shows one manga's chapter list, and downloads a chapter as a CBZ (skipping
the network entirely if it's already downloaded) then opens it. Bookmarking
a manga to the library happens from searchbrowser.lua's result list, not
here.

The title bar's left "hamburger" button (same technique as
librarybrowser.lua's own onLeftButtonTap) groups the chapter sort-order
toggle and bulk-downloading the next N chapters from wherever the user last
left off reading (see downloadNext()).

reload() renders the local downloads index first (see the note there) and
only opportunistically refreshes from the network's "manga" command (details
+ chapters together) when already connected, so already-downloaded chapters
stay browsable/openable fully offline.

Requires both source_path (the installed .aix's path, used for every
content operation -- search/mangaUpdate/download) and source_key (the
source's stable manifest ID, used for every downloads-index operation --
see the downloads Go package doc for why those are kept separate). Callers
that only have one of the two (e.g. librarybrowser.lua's bookmark entries,
which store source_key and must re-resolve source_path via
installedsources.lua's findByKey before opening this) must resolve the
other first.
]]

local ButtonDialog = require("ui/widget/buttondialog")
local ChapterOrder = require("chapterorder")
local ConfirmBox = require("ui/widget/confirmbox")
local InfoMessage = require("ui/widget/infomessage")
local InputDialog = require("ui/widget/inputdialog")
local Menu = require("ui/widget/menu")
local NetworkMgr = require("ui/network/manager")
local OpenWidgets = require("openwidgets")
local Prefetch = require("prefetch")
local Trapper = require("ui/trapper")
local UIManager = require("ui/uimanager")
local lfs = require("libs/libkoreader-lfs")
local util = require("util")
local T = require("ffi/util").template
local _ = require("gettext")

local MangaBrowser = Menu:extend{}

local mangaLabel = Prefetch.mangaLabel
local chapterLabel = Prefetch.chapterLabel

function MangaBrowser:init()
    self.title = mangaLabel(self.manga)
    self.subtitle = self.source_name
    self.chapters = {}
    self.downloaded_keys = {} -- chapter Key -> local path, refreshed by reload()
    self.item_table = {}
    -- The hamburger menu (see onLeftButtonTap below) groups the sort-order
    -- toggle and bulk-download -- same technique as librarybrowser.lua's
    -- own title_bar_left_icon/onLeftButtonTap.
    self.title_bar_left_icon = "appbar.menu"
    Menu.init(self)
    OpenWidgets.push(self)
    -- See the same note in repobrowser.lua's init(): defer past the
    -- caller's own UIManager:show(self), since reload()'s progress widget
    -- would otherwise show (and get buried) first.
    UIManager:nextTick(function() self:reload() end)
end

-- See librarybrowser.lua's onCloseWidget for why this is needed.
function MangaBrowser:onCloseWidget()
    Menu.onCloseWidget(self)
    OpenWidgets.remove(self)
end

-- The hamburger menu (see title_bar_left_icon above) groups the sort-order
-- toggle (moved here from its own dedicated button -- its current state
-- shows as this button's label, same as librarybrowser.lua's "Sort: X"
-- convention, so no separate toast is needed on toggle) and bulk-downloading
-- the next N chapters (see downloadNext()). Anchored under the hamburger
-- icon itself, same technique as librarybrowser.lua:82-136.
function MangaBrowser:onLeftButtonTap()
    local dialog
    dialog = ButtonDialog:new{
        shrink_unneeded_width = true,
        anchor = function() return self.title_bar.left_button.image.dimen end,
        buttons = {
            {{
                text = self.store:sortOrder() == "asc" and _("Sort: Oldest first") or _("Sort: Newest first"),
                callback = function()
                    UIManager:close(dialog)
                    self.store:setSortOrder(self.store:sortOrder() == "asc" and "desc" or "asc")
                    self:refresh()
                end,
            }},
            {{
                text = _("Download next chapters…"),
                callback = function()
                    UIManager:close(dialog)
                    self:promptBulkDownload()
                end,
            }},
        },
    }
    UIManager:show(dialog)
end

-- downloadedPath returns the local CBZ path for chapter_key if it's
-- downloaded and the file still exists, or "" otherwise. Reads
-- self.downloaded_keys (an in-memory snapshot refreshed by reload() and
-- kept up to date locally after each download/removal here) rather than
-- querying the downloads index per chapter -- this is called once per row
-- while building the chapter list, and a manga can have dozens of
-- chapters, so a subprocess call per row is not an option. The lfs check
-- is a cheap local stat (no subprocess) so it's fine to do per row; a
-- stale entry (file deleted out from under us) is just treated as "not
-- downloaded" here rather than cleaned up, since that too would need a
-- subprocess call mid-render -- record() overwrites it on the next real
-- download regardless.
function MangaBrowser:downloadedPath(chapter_key)
    local path = self.downloaded_keys[chapter_key]
    if not path or path == "" then
        return ""
    end
    if lfs.attributes(path, "mode") ~= "file" then
        return ""
    end
    return path
end

function MangaBrowser:genItemTable()
    local item_table = {}
    local sort_order = self.store:sortOrder()
    local ordered = self.chapters
    if sort_order == "asc" then
        ordered = {}
        for i = #self.chapters, 1, -1 do
            table.insert(ordered, self.chapters[i])
        end
    end
    for _idx, chapter in ipairs(ordered) do
        local mandatory_parts = {}
        if self.store:isChapterRead(self.source_key, self.manga.Key, chapter.Key) then
            table.insert(mandatory_parts, _("Read"))
        end
        if self:downloadedPath(chapter.Key) ~= "" then
            table.insert(mandatory_parts, "✓")
        end
        table.insert(item_table, {
            text = chapterLabel(chapter),
            mandatory = table.concat(mandatory_parts, " "),
            chapter = chapter,
        })
    end
    return item_table
end

function MangaBrowser:refresh()
    self.item_table = self:genItemTable()
    self:updateItems()
end

-- reloadDownloadedKeys fetches the full downloads index (unless a
-- previously-fetched list is passed in, to avoid a second subprocess call
-- when reload() already has one) and keeps just this manga's entries, so
-- per-row downloadedPath() lookups are local table reads instead of one
-- subprocess call each -- see the note on this in downloadedPath(). Returns
-- the full (unfiltered) list, for callers that also need it (reload()'s
-- offline chapter-list fallback).
function MangaBrowser:reloadDownloadedKeys(all)
    all = all or self.downloads_engine:list()
    self.downloaded_keys = {}
    if not all then
        return all
    end
    for _, entry in ipairs(all) do
        if entry.sourceKey == self.source_key and entry.mangaKey == self.manga.Key then
            self.downloaded_keys[entry.chapterKey] = entry.path
        end
    end
    return all
end

-- reload() always shows what's already downloaded first, straight from the
-- local downloads index -- no network involved -- so this manga's
-- downloaded chapters stay browsable/openable even fully offline. It then
-- opportunistically refreshes from the network (fetching the real chapter
-- list, titles, and any chapters not yet downloaded) only if already
-- connected; if not, it skips straight past that rather than popping a
-- "connect to network?" prompt, since the point is to let already-downloaded
-- content just work without a network fuss.
--
-- Every exit path ends with promptResume() (see below) so the "Resume
-- reading?" prompt appears once chapter data is actually ready, whether that
-- data came from the offline/local-only path or the online refresh.
function MangaBrowser:reload()
    Trapper:wrap(function()
        local all = self:reloadDownloadedKeys()
        if #self.chapters == 0 then
            self.chapters = ChapterOrder.fromEntries(all, self.source_key, self.manga.Key)
        end
        self:refresh()

        if not NetworkMgr:isConnected() then
            self:promptResume()
            return
        end

        local updated, err = self.engine:mangaUpdate(self.source_path, self.manga.Key)
        if not updated then
            if #self.chapters == 0 then
                UIManager:show(InfoMessage:new{ text = T(_("Could not load chapters:\n%1"), err) })
            end
            self:promptResume()
            return
        end
        self.manga = updated
        self.chapters = type(updated.Chapters) == "table" and updated.Chapters or {}
        self:reloadDownloadedKeys()
        self:refresh()
        self:promptResume()
    end)
end

-- resumeChapter returns the chapter to offer resuming into: the one right
-- after the last chapter marked read (see findLastReadChapter above), same
-- resume point "Download next chapters…" already uses. nil if nothing's
-- been read yet (nothing to "continue") or the reader is already caught up
-- -- both cases mean promptResume() below shows nothing.
function MangaBrowser:resumeChapter()
    local ordered = ChapterOrder.orderedByReading(self.chapters)
    if #ordered == 0 then
        return nil
    end
    local last_key = self:findLastReadChapter(ordered)
    if not last_key then
        return nil
    end
    local upcoming = ChapterOrder.after(self.chapters, last_key, 1)
    return upcoming[1]
end

-- promptResume shows a "Resume reading from <chapter>?" confirmation once
-- reload() has finished loading chapter data (see the note there). Accepting
-- runs the exact same open path as tapping that chapter's row would
-- (onMenuSelect) -- download-if-needed, open, prefetch-ahead -- so there's
-- no separate open logic to keep in sync.
function MangaBrowser:promptResume()
    local chapter = self:resumeChapter()
    if not chapter then
        return
    end
    UIManager:show(ConfirmBox:new{
        text = T(_("Resume reading from %1?"), chapterLabel(chapter)),
        ok_text = _("Resume"),
        cancel_text = _("Not now"),
        ok_callback = function()
            self:onMenuSelect({ chapter = chapter })
        end,
    })
end

-- prefetchAhead silently downloads up to store:bufferChapters() upcoming
-- chapters (in reading order, not display sort order) after chapter, so
-- they're likely already local by the time the reader reaches them. See
-- prefetch.lua -- shared with nextchapter.lua's end-of-book auto-advance,
-- so the buffer keeps refilling as the user reads through, not just on the
-- first chapter opened from this list.
function MangaBrowser:prefetchAhead(chapter)
    Prefetch.ahead({
        engine = self.engine,
        downloads_engine = self.downloads_engine,
        store = self.store,
        downloads_dir = self.downloads_dir,
        source_path = self.source_path,
        source_key = self.source_key,
        manga = self.manga,
    }, self.chapters, chapter.Key,
    function(c, path)
        self.downloaded_keys[c.Key] = path
    end,
    function(any_new)
        if any_new then
            self:refresh()
        end
    end)
end

-- findLastReadChapter returns the Key of the last chapter (in ordered,
-- ascending reading order) marked read via store:markChapterRead (set only
-- on end-of-book -- see the note on this in store.lua), or nil if none of
-- them are. There's no direct "last read chapter" pointer in store.lua
-- (only this per-chapter boolean and a separate manga-level lastReadAt
-- timestamp used for library sorting) -- this is how downloadNext() finds
-- where to resume.
function MangaBrowser:findLastReadChapter(ordered)
    local last_key
    for _, c in ipairs(ordered) do
        if self.store:isChapterRead(self.source_key, self.manga.Key, c.Key) then
            last_key = c.Key
        end
    end
    return last_key
end

-- downloadNext downloads up to n chapters in reading order, resuming from
-- just after the last chapter marked read (or from the very first chapter
-- if none are). Chapters already downloaded within that range are skipped
-- (not re-downloaded) but still counted as part of the batch. Unlike
-- prefetchAhead's silent background buffer, this is a visible,
-- user-initiated action -- each chapter gets Engine:download's normal
-- progress popup (dismissable/cancellable -- see the note on this in
-- engine.lua), shown with the batch's position via progress_text_override,
-- and cancelling one stops the whole batch rather than skipping to the
-- next chapter.
function MangaBrowser:downloadNext(n)
    local ordered = ChapterOrder.orderedByReading(self.chapters)
    if #ordered == 0 then
        UIManager:show(InfoMessage:new{ text = _("No numbered chapters to download."), timeout = 2 })
        return
    end

    local last_key = self:findLastReadChapter(ordered)
    local upcoming
    if last_key then
        upcoming = ChapterOrder.after(self.chapters, last_key, n)
    else
        -- ChapterOrder.after returns {} for a nil current_key rather than
        -- falling back to the start (it only ever looks for a match) --
        -- "nothing read yet" has to be handled here instead.
        upcoming = {}
        for i = 1, math.min(n, #ordered) do
            table.insert(upcoming, ordered[i])
        end
    end
    if #upcoming == 0 then
        UIManager:show(InfoMessage:new{ text = _("You're all caught up -- no next chapter yet."), timeout = 2 })
        return
    end

    local needs_download = false
    for _, c in ipairs(upcoming) do
        if self:downloadedPath(c.Key) == "" then
            needs_download = true
            break
        end
    end
    if not needs_download then
        -- The whole requested range is already local -- skip the network
        -- gate below entirely rather than prompting to connect for a batch
        -- that turns out to be a no-op.
        UIManager:show(InfoMessage:new{ text = _("Already downloaded."), timeout = 2 })
        return
    end

    NetworkMgr:runWhenConnected(function()
        Trapper:wrap(function()
            local downloaded_any = false
            for i, c in ipairs(upcoming) do
                if self:downloadedPath(c.Key) ~= "" then
                    downloaded_any = true
                else
                    local chapter_filename = util.getSafeFilename(chapterLabel(c) .. ".cbz", self.downloads_dir)
                    local out_path = self.downloads_dir .. "/" .. chapter_filename
                    local manga_dir_name = mangaLabel(self.manga) .. " [" .. self.source_key .. "]"
                    local progress = T(_("Downloading %1/%2: %3"), i, #upcoming, chapterLabel(c))
                    local path, err = self.engine:download(
                        self.source_path, self.manga.Key, c.Key, out_path, self.downloads_dir, false, progress, manga_dir_name)
                    if not path then
                        if err == _("Cancelled") then
                            -- Distinct from the "Download failed" every
                            -- other caller shows on cancel -- cancelling a
                            -- bulk action isn't an error, it's the user
                            -- stopping partway through on purpose.
                            UIManager:show(InfoMessage:new{
                                text = T(_("Cancelled -- downloaded %1 of %2 chapters."), i - 1, #upcoming),
                            })
                        else
                            UIManager:show(InfoMessage:new{ text = T(_("Download failed:\n%1"), err) })
                        end
                        break
                    end
                    self.downloaded_keys[c.Key] = path
                    downloaded_any = true
                end
            end
            if downloaded_any then
                self.downloads_engine:prune(self.store:downloadLimitBytes())
            end
            self:refresh()
        end)
    end)
end

-- promptBulkDownload asks how many upcoming chapters to download (see
-- downloadNext()), reusing settingsbrowser.lua's numeric InputDialog shape.
function MangaBrowser:promptBulkDownload()
    if #self.chapters == 0 then
        -- reload() populates self.chapters asynchronously (deferred via
        -- UIManager:nextTick past init()) -- the hamburger menu can in
        -- principle be opened before that's landed.
        UIManager:show(InfoMessage:new{ text = _("No chapters loaded yet -- try again in a moment."), timeout = 2 })
        return
    end
    local dialog
    dialog = InputDialog:new{
        title = _("Download next chapters"),
        description = _("How many chapters to download, starting after the last one you've read (or from the first chapter if you haven't read any yet)."),
        input = "5",
        input_type = "number",
        buttons = {{
            {
                text = _("Cancel"),
                id = "close",
                callback = function() UIManager:close(dialog) end,
            },
            {
                text = _("Download"),
                is_enter_default = true,
                callback = function()
                    local n = tonumber(dialog:getInputText())
                    UIManager:close(dialog)
                    if n and n >= 1 then
                        self:downloadNext(math.floor(n))
                    end
                end,
            },
        }},
    }
    UIManager:show(dialog)
    dialog:onShowKeyboard()
end

function MangaBrowser:openLocal(path)
    self.store:markLastRead(self.source_key, self.manga.Key)
    UIManager:close(self)
    if self.ui.document then
        self.ui:switchDocument(path)
    else
        self.ui:openFile(path)
    end
end

function MangaBrowser:downloadAndOpen(chapter)
    NetworkMgr:runWhenConnected(function()
        Trapper:wrap(function()
            local chapter_filename = util.getSafeFilename(chapterLabel(chapter) .. ".cbz", self.downloads_dir)
            local out_path = self.downloads_dir .. "/" .. chapter_filename
            local manga_dir_name = mangaLabel(self.manga) .. " [" .. self.source_key .. "]"

            local path, err = self.engine:download(
                self.source_path, self.manga.Key, chapter.Key, out_path, self.downloads_dir, nil, nil, manga_dir_name)
            if not path then
                UIManager:show(InfoMessage:new{ text = T(_("Download failed:\n%1"), err) })
                return
            end
            self.downloaded_keys[chapter.Key] = path
            -- The storage-limit prune is a persistent-storage concept -- under
            -- Ephemeral Mode, removeAllExcept() below already keeps at most
            -- ~1-2 chapters resident, and running prune() on top of that risks
            -- deleting the chapter just downloaded before it's even opened, if
            -- the user's saved limit happens to be smaller than that.
            if not self.store:isEphemeralMode() then
                self.downloads_engine:prune(self.store:downloadLimitBytes())
            end
            self:refresh()
            self:openLocal(path)
            if self.store:isEphemeralMode() then
                self.downloads_engine:removeAllExcept(path)
            end
            -- Deferred so the reader's own widget/gesture setup finishes and
            -- is topmost before prefetch's Trapper-wrapped subprocess calls
            -- start stacking dismissablePopen widgets on top of it -- doing
            -- this inline was swallowing the first several page-turn taps
            -- after opening a freshly-downloaded chapter.
            UIManager:nextTick(function() self:prefetchAhead(chapter) end)
        end)
    end)
end

function MangaBrowser:onMenuSelect(item)
    local existing = self:downloadedPath(item.chapter.Key)
    if existing ~= "" then
        self:openLocal(existing)
        -- Under Ephemeral Mode this chapter is commonly one Prefetch already
        -- fetched ahead of time (see CLAUDE.md), so it's opened from here,
        -- not downloadAndOpen() -- needs the same cleanup call, or a
        -- prefetched-then-opened chapter would never get its predecessor
        -- cleaned up at all.
        if self.store:isEphemeralMode() then
            self.downloads_engine:removeAllExcept(existing)
        end
        -- See the note on this in downloadAndOpen() -- deferred so it
        -- doesn't steal early page-turn taps from the just-opened reader.
        UIManager:nextTick(function() self:prefetchAhead(item.chapter) end)
        return true
    end

    self:downloadAndOpen(item.chapter)
    return true
end

function MangaBrowser:onMenuHold(item)
    local existing = self:downloadedPath(item.chapter.Key)
    if existing == "" then
        return true
    end

    local dialog
    dialog = ButtonDialog:new{
        title = chapterLabel(item.chapter),
        buttons = {
            {{
                text = _("Re-download"),
                callback = function()
                    UIManager:close(dialog)
                    Trapper:wrap(function()
                        self.downloads_engine:remove(self.source_key, self.manga.Key, item.chapter.Key)
                        self.downloaded_keys[item.chapter.Key] = nil
                        self:downloadAndOpen(item.chapter)
                    end)
                end,
            }},
            {{
                text = _("Remove download"),
                callback = function()
                    UIManager:close(dialog)
                    Trapper:wrap(function()
                        self.downloads_engine:remove(self.source_key, self.manga.Key, item.chapter.Key)
                        self.downloaded_keys[item.chapter.Key] = nil
                        self:refresh()
                    end)
                end,
            }},
        },
    }
    UIManager:show(dialog)
    return true
end

return MangaBrowser
