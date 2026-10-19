#!/bin/sh
# Compose the contact sheets from a run.sh capture directory.
#
#	sh tools/appkitscenes/sheets.sh CAPTURES_DIR SHEETS_DIR
#
# Per scene: light and dark rows, the four phases as columns. An overview of
# the four scenes, and a sheet of 3x crops of the window-level details.
set -eu
in=$1 out=$2
here=$(cd "$(dirname "$0")" && pwd)
bin=$here/bin/AppKitScenes.app/Contents/MacOS/AppKitScenes
mkdir -p "$out" "$out/crops"
os=$(sed -n 's/^    "os" : "\(.*\)",*$/\1/p' "$in/finder-light-active-top.json" | head -n 1)

for s in finder notes settings chat; do
	m=$out/$s.json
	printf '{"title": "%s — macOS %s, AppKit reference scene (light above, dark below)", "columns": 4, "scale": 0.5, "cells": [' "$s" "$os" >"$m"
	sep=
	for ap in light dark; do
		for ph in active-top active-scrolled inactive-top inactive-scrolled; do
			printf '%s{"label": "%s %s", "path": "%s"}' "$sep" "$ap" "$ph" "$in/$s-$ap-$ph.png" >>"$m"
			sep=,
		done
	done
	printf ']}\n' >>"$m"
	"$bin" -sheet "$m" "$out/sheet-$s.png"
	rm "$m"
done

m=$out/overview.json
printf '{"title": "Four reference scenes — macOS %s, active, light and dark", "columns": 4, "scale": 0.5, "cells": [' "$os" >"$m"
sep=
for ap in light dark; do
	for s in finder notes settings chat; do
		printf '%s{"label": "%s %s", "path": "%s"}' "$sep" "$s" "$ap" "$in/$s-$ap-active-top.png" >>"$m"
		sep=,
	done
done
printf ']}\n' >>"$m"
"$bin" -sheet "$m" "$out/sheet-overview.png"
rm "$m"

# crops: name capture x y w h (points), zoomed 3x
crop() {
	"$bin" -crop "$in/$2.png" "$3" "$4" "$5" "$6" 3 "$out/crops/$1.png"
}
for ap in light dark; do
	crop "toolbar-left-$ap-active" "finder-$ap-active-top" 0 0 520 60
	crop "toolbar-left-$ap-inactive" "finder-$ap-inactive-top" 0 0 520 60
	crop "toolbar-right-$ap-active" "finder-$ap-active-top" 760 0 360 60
	crop "sidebar-$ap-unemphasised" "finder-$ap-active-top" 0 40 320 220
	crop "sidebar-$ap-emphasised" "settings-$ap-active-top" 0 80 215 340
	crop "sidebar-$ap-inactive" "settings-$ap-inactive-top" 0 80 215 340
	crop "notes-rows-$ap" "notes-$ap-active-top" 0 30 240 200
	crop "table-selection-$ap" "finder-$ap-active-top" 160 50 520 130
	crop "pocket-notes-$ap-active" "notes-$ap-active-scrolled" 240 0 420 70
	crop "pocket-notes-$ap-inactive-top" "notes-$ap-inactive-top" 240 0 420 70
	crop "pocket-chat-$ap" "chat-$ap-active-scrolled" 560 0 340 90
	crop "composer-$ap" "chat-$ap-active-scrolled" 280 520 620 100
	crop "form-$ap" "settings-$ap-active-top" 215 40 605 260
done
m=$out/details.json
printf '{"title": "Window-level details, 3x crops (macOS %s): light left, dark right", "columns": 4, "scale": 0.34, "cells": [' "$os" >"$m"
sep=
for n in toolbar-left-%s-active toolbar-left-%s-inactive toolbar-right-%s-active table-selection-%s sidebar-%s-unemphasised sidebar-%s-emphasised sidebar-%s-inactive notes-rows-%s pocket-notes-%s-active pocket-notes-%s-inactive-top pocket-chat-%s composer-%s form-%s; do
	for ap in light dark; do
		f=$(printf "$n" "$ap")
		printf '%s{"label": "%s", "path": "%s"}' "$sep" "$f" "$out/crops/$f.png" >>"$m"
		sep=,
	done
done
printf ']}\n' >>"$m"
"$bin" -sheet "$m" "$out/sheet-details.png"
rm "$m"
