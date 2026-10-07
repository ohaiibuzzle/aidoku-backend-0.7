--[[--
Plugin-local gettext. Use exactly like KOReader's own:

    local _ = require("aidoku_l10n")
    UIManager:show(InfoMessage:new{ text = _("No results.") })

Lookup order: this plugin's l10n/<lang>/aidoku.po, then KOReader's own
gettext (which already translates common strings like "Cancel"/"Settings"),
then the English msgid itself.

Why not just GetText.loadMO() our own catalog: KOReader's gettext is a
single process-wide table, and loadMO merges into it -- our msgstrs would
silently override KOReader's for any shared msgid ("Remove", "Cancel", ...)
everywhere in the app, and a language switch's changeLang() wipes them
again. Parsing the .po ourselves keeps the two catalogs separate and needs
no msgfmt compile step.

Only plain msgid/msgstr entries are supported (fuzzy and plural entries are
skipped). _.ngettext/_.pgettext fall through to KOReader's gettext
unchanged, so they work but only see KOReader's catalog.

See koreader/update-strings.sh for regenerating l10n/aidoku.pot.
]]

local gettext = require("gettext")

local DOMAIN = "aidoku"

-- plugin_dir is wherever this file lives, so it works regardless of which
-- plugins/ directory KOReader loaded us from.
local plugin_dir = (debug.getinfo(1, "S").source:match("^@(.*)/[^/]*$")) or "."

local function unescape(s)
    return (s:gsub("\\(.)", { n = "\n", t = "\t", ['"'] = '"', ["\\"] = "\\" }))
end

-- parsePO returns a { [msgid] = msgstr } table, or nil if path can't be
-- opened. Deliberately small: enough for what xgettext/msgmerge emit for
-- this plugin, not a general .po implementation.
local function parsePO(path)
    local f = io.open(path, "r")
    if not f then
        return nil
    end
    local catalog = {}
    local entry, field, fuzzy = {}, nil, false

    local function flush()
        if not fuzzy and not entry.plural and entry.msgid and entry.msgid ~= ""
            and entry.msgstr and entry.msgstr ~= "" then
            catalog[entry.msgid] = entry.msgstr
        end
        entry, field, fuzzy = {}, nil, false
    end

    for line in f:lines() do
        if line:match("^%s*$") then
            flush()
        elseif line:match("^#,.*fuzzy") then
            fuzzy = true
        elseif line:match("^#") then -- luacheck: ignore 542
            -- other comments
        else
            local key, str = line:match('^(%S+)%s+"(.*)"%s*$')
            if key then
                if key == "msgid" and entry.msgstr then
                    flush() -- new entry without a separating blank line
                end
                if key == "msgid_plural" or key:match("^msgstr%[") then
                    entry.plural = true
                end
                field = key
                entry[field] = unescape(str)
            else
                local cont = line:match('^%s*"(.*)"%s*$')
                if cont and field then
                    entry[field] = entry[field] .. unescape(cont)
                end
            end
        end
    end
    flush()
    f:close()
    return catalog
end

-- loadCatalog tries the exact language first ("pt_BR"), then its base
-- language ("pt").
local function loadCatalog(lang)
    if not lang or lang == "C" then
        return {}
    end
    local candidates = { lang }
    local base = lang:match("^(%a+)_")
    if base then
        table.insert(candidates, base)
    end
    for _idx, l in ipairs(candidates) do
        local catalog = parsePO(plugin_dir .. "/l10n/" .. l .. "/" .. DOMAIN .. ".po")
        if catalog then
            return catalog
        end
    end
    return {}
end

-- KOReader only applies a UI language change after a restart, which also
-- reloads this module, so the catalog is loaded once at require() time.
local catalog = loadCatalog(gettext.current_lang)

return setmetatable({}, {
    __call = function(_self, msgid)
        return catalog[msgid] or gettext(msgid)
    end,
    __index = gettext,
})
