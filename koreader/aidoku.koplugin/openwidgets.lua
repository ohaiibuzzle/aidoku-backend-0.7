--[[--
Process-wide (require()-cached) registry of every currently-shown top-level
Aidoku screen widget (LibraryBrowser, MangaBrowser, SettingsBrowser, ...),
in the order they were opened.

Each of those screens is its own independent UIManager top-level widget --
shown via UIManager:show(...), not a child of the FileManager/ReaderUI that
opened it. Drilling deeper (e.g. Library -> Manga -> a chapter) only closes
the screen you're directly leaving; an ancestor further back (Library) is
just left behind, buried under whatever's shown next, still sitting on
UIManager's window stack.

main.lua's hookClose calls closeAll() right before the owning FileManager/
ReaderUI instance's own onClose() runs (Exit, Restart, or the Reader<->
FileManager handoff), closing every tracked Aidoku screen first. Without
this, UIManager's window stack never becomes empty after a left-open Aidoku
screen -- and KOReader's main loop only stops "when we have no window to
show" -- so KOReader doesn't actually exit: the leftover screen (e.g.
Library, resurfacing once the Reader on top of it closes) is left on screen
requiring a manual close, and repeated visits without backing out can stack
up multiple orphaned screens across one session.
]]

local stack = {}

local M = {}

function M.push(widget)
    table.insert(stack, widget)
end

function M.remove(widget)
    for i = #stack, 1, -1 do
        if stack[i] == widget then
            table.remove(stack, i)
            return
        end
    end
end

-- closeAll closes every tracked screen, most-recently-opened first, so
-- widgets stacked on top of an ancestor go before that ancestor. Each
-- screen's own onCloseWidget calls remove() during this, which is safe
-- since we've already popped it off stack before closing it.
function M.closeAll()
    local UIManager = require("ui/uimanager")
    while #stack > 0 do
        local widget = table.remove(stack)
        UIManager:close(widget)
    end
end

return M
