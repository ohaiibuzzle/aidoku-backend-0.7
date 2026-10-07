#!/bin/sh
# Regenerates aidoku.koplugin/l10n/aidoku.pot from every _("...") call in the
# plugin's Lua files, then merges it into each existing translation
# (l10n/<lang>/aidoku.po) so new/changed strings show up there as untranslated
# or fuzzy. Needs GNU gettext (xgettext, msgmerge): `brew install gettext` or
# `apt-get install gettext`.
#
# To start a new translation:
#   msginit -i aidoku.koplugin/l10n/aidoku.pot -l de \
#     -o aidoku.koplugin/l10n/de/aidoku.po --no-translator
# Language directory names must match KOReader's own (its l10n/<lang>
# folders, e.g. "de", "pt_BR", "zh_CN") -- see aidoku_l10n.lua.
set -eu

plugin_dir="$(cd "$(dirname "$0")" && pwd)/aidoku.koplugin"
pot="$plugin_dir/l10n/aidoku.pot"
mkdir -p "$plugin_dir/l10n"

cd "$plugin_dir"
# _meta.lua is excluded: KOReader's plugin loader reads it with its own
# gettext before any plugin module (including aidoku_l10n) is loaded.
find . -maxdepth 1 -name '*.lua' ! -name '_meta.lua' | sort | xargs \
    xgettext --language=Lua --keyword=_ --from-code=UTF-8 \
        --add-comments=TRANSLATORS: --sort-by-file \
        --package-name=aidoku.koplugin \
        -o "$pot"

for po in l10n/*/aidoku.po; do
    [ -e "$po" ] || continue
    msgmerge --quiet --update --backup=none "$po" "$pot"
    echo "merged $po"
done

echo "wrote $pot ($(grep -c '^msgid "' "$pot") strings)"
