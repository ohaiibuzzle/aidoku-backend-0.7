--[[--
Tiny shared flag bridging ReaderUI's Home/Files action to the next
FileManager-context Aidoku plugin instance it spins up. require() caches
this table process-wide, so setting it from the reader-side hook in
main.lua and reading it from the file-manager-side instance's init()
communicates across the teardown/recreate boundary between those two
plugin instances -- see Aidoku:hookHome()/Aidoku:openPendingLibrary().
]]

return { open = false }
