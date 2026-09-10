--[[--
Process-wide registry of every currently-shown top-level Aidoku screen
(LibraryBrowser, MangaBrowser, SettingsBrowser, ...), in open order.

Each screen is its own independent UIManager top-level widget, not a
child of the FileManager/ReaderUI that opened it -- drilling deeper only
closes the screen being left, leaving ancestors buried on the window
stack. main.lua's hookClose calls closeAll() right before Exit/Restart/the
Reader<->FileManager handoff, since a leftover screen otherwise keeps
UIManager's stack non-empty and KOReader never actually exits.
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
