#!/bin/sh
# Compose the side-by-side sheets: AppKit (left) next to ours (right), per
# control group, light and dark.
#
#	sh tools/appkitref/sheets.sh REF_DIR OURS_DIR OUT_DIR
#
# REF_DIR is an AppKitRef -o directory, OURS_DIR a tools/appkitref/ours -o
# directory; both hold light/ and dark/ PNGs named after the samples.
set -eu
ref=$1 ours=$2 out=$3
here=$(cd "$(dirname "$0")" && pwd)
bin=$here/bin/AppKitRef.app/Contents/MacOS/AppKitRef
mkdir -p "$out"

group() { # group NAME TITLE sample...
	name=$1 title=$2
	shift 2
	for mode in light dark; do
		m=$out/$name-$mode.json
		dark=false
		[ $mode = dark ] && dark=true
		printf '{"title": "%s — %s", "dark": %s, "rows": [' "$title" "$mode" "$dark" >"$m"
		sep=
		for s in "$@"; do
			printf '%s{"label": "%s", "left": "%s", "right": "%s"}' "$sep" "$s" "$ref/$mode/$s.png" "$ours/$mode/$s.png" >>"$m"
			sep=,
		done
		printf ']}\n' >>"$m"
		"$bin" -sheet "$m" "$out/$name-$mode.png"
		rm "$m"
	done
}

group buttons "Push buttons" push-mini push-small push-regular push-large push-xlarge push-default push-default-small push-default-inactive push-pressed push-default-pressed push-disabled push-default-disabled push-focused push-destructive push-glass
group icons "Icon buttons" icon-rounded icon-toolbar icon-borderless
group choices "Checkbox, radio, switch" checkbox-off checkbox-on checkbox-mixed checkbox-on-disabled checkbox-off-disabled checkbox-off-pressed checkbox-on-focused checkbox-on-small checkbox-on-large radio-off radio-on radio-on-disabled radio-on-focused switch-off switch-on switch-on-disabled switch-on-focused switch-on-small switch-on-mini switch-on-large
group selects "Pop-up, segmented, slider" popup popup-small popup-disabled popup-focused pulldown popup-borderless segmented segmented-small slider slider-small slider-disabled slider-focused
group fields "Text and search fields" field-placeholder field-text field-focused field-disabled field-small field-large search-empty search-text search-focused
group containers "Separator, box, type" separator box label-styles
