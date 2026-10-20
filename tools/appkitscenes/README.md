# AppKitScenes

A dev tool for measuring how a current macOS app *window* is laid out and lit,
not framework code. Like `tools/appkitref`, it is Objective-C. `appkitref`
renders single controls offscreen. This tool shows real windows on screen, so
that the window server composites the Liquid Glass. GnuiShot captures them,
and the tool dumps its own live view hierarchy as JSON.

The results and what they mean are in `docs/design/liquid-glass-reference.md`.

## Scenes

The scenes are pure AppKit in Apple's standard idioms, with fixed sample
content and no user data:

| Scene | Built from | Target |
|---|---|---|
| `finder` | `NSSplitViewController`: a source-list sidebar (sections, tags), an inset `NSTableView` with a header and stripes, an inspector item. A unified toolbar: sidebar toggle, back/forward (navigational group), a select-one view group, menu items, a grouped share/tag capsule, a search item, both tracking separators, an inspector toggle | Pilot |
| `notes` | A sidebar list of notes (two-line rows, date sections), an `NSTextView` editor. A unified *compact* toolbar | `examples/notes` |
| `settings` | A sidebar with a search field and System-Settings-style icon tiles, and a form of `NSBox` group boxes with `NSPopUpButton`, `NSSwitch` and `NSSlider`. Back/forward and the title in the toolbar | gallery showcases |
| `chat` | A sidebar of conversations (avatar rows) and a search field; a message area (bubbles drawn by the tool); an `NSGlassEffectView` composer in a bottom `NSSplitViewItemAccessoryViewController` | ZapFast |

Each scene walks four phases: `inactive-top`, `inactive-scrolled`,
`active-top` and `active-scrolled`. "Scrolled" puts content under the toolbar,
for the scroll-edge effect. The tool starts in the background (`open -g`), so
the user's app keeps focus, and it activates itself only for the two active
phases, then quits, which returns focus. Light or dark is `NSApp.appearance`.
It changes no setting.

## Build and run

```sh
bash tools/appkitscenes/build.sh            # -> tools/appkitscenes/bin/AppKitScenes.app (ad-hoc signed, dev.gonativeui.appkitscenes)
scp -r tools/appkitscenes/bin/AppKitScenes.app tools/appkitscenes/run.sh mac:gnui-test/appkitscenes/
ssh mac 'cd ~/gnui-test/appkitscenes && sh run.sh /tmp/gnui-scenes/final [scene ...]'
```

`run.sh` runs on the test Mac. It needs GnuiShot installed at
`~/gnui-test/shot.sh` with its Screen Recording grant (see
`tools/gnuishot/README.md`; do not rebuild GnuiShot). Per scene and
appearance it:

1. launches the tool;
2. waits for each phase's `.ready` file;
3. captures the window by its id at 2×, without the shadow;
4. copies the dump;
5. writes `.done`.

Windows appear for a few seconds each; all four scenes take about two minutes.

The run takes some options:

- **`TAG=name EXTRA="-Key value"`** runs a variant: `EXTRA` goes to the tool's argument domain (that process only), and the files are named `<scene>_<tag>-…`.
- **`PHASES="inactive-scrolled inactive-top"`** picks and orders the phases.

`run.sh` always passes `-AppleAccentColor 4`, the default blue.

`-width W` (the tool's own argument) lays the scene out W points wide.
`toolbarprobe.m` is a separate probe: it lays out a toolbar of given items
offscreen and prints its item viewers', platters' and buttons' frames (how
AppKit sizes toolbar items; build and usage in its header).

The tool itself:

```
AppKitScenes -scene finder|notes|settings|chat -appearance light|dark -hs DIR [-offscreen] [-tag T] [-phases a,b,c]
```

`-offscreen` builds the window without ordering it on screen and dumps every
phase. It is useful to check a scene's construction locally. Toolbar and glass
geometry differ offscreen (the sidebar glass is not laid out the same), so
measure only live windows.

## Output

Per phase, `<scene>-<appearance>-<phase>.json` holds, in points from the window's top-left corner:

- the system, the accent and the accessibility flags;
- the window: frame, `contentLayoutRect`, titlebar + toolbar height, traffic lights, key/main/active, first responder, and the private `_cornerRadius` (read only, as in `appkitref`);
- each split item: behaviour, frame, safe area, accessories, and the superview chain with layers and `effectiveCornerRadii`;
- the toolbar items;
- every table and outline: style, row size, and each visible row's rect, cell, text frame, font and colour, and selection;
- semantic colours resolved in the window's appearance;
- `tree`, the whole view hierarchy from the theme frame down: class, frame, layer (radius, colours, filters), `effectiveCornerRadii` and `cornerConfiguration` (macOS 27), glass (radius, style, tint), visual-effect material and state, text, fonts and colours, scroll insets.

The PNG beside it is GnuiShot's capture; `.shot.json` is GnuiShot's result.

## Pixel tools

Coordinates are points from the window's top-left; captures are 2×.

```
AppKitScenes -measure DIR                       DIR/measured.json for every capture with a dump (below)
AppKitScenes -radius IN.png X Y W H             body edges and the fitted top-left corner radius of a rounded rect near the rect
AppKitScenes -probe IN.png X Y [X Y ...]        sRGB colours at points
AppKitScenes -profile IN.png X0 Y0 X1 Y1 [STEP] colours along a line
AppKitScenes -crop IN.png X Y W H ZOOM OUT.png  an enlarged region
AppKitScenes -sheet MANIFEST.json OUT.png       a contact sheet ({title, columns, scale, cells: [{label, path}]})
```

`-measure` records, per capture:

- the window mask's corner (the 45° alpha crossing);
- the traffic-light colours;
- each pane's fill (median and luminance range), and the pixels across the sidebar divider;
- the content's toolbar band, from a column clear of toolbar items, with the pixel column at the toolbar's bottom edge (the hairline);
- the bottom accessory's edge;
- every toolbar glass capsule: its fill, the pixels across its rim, the column below it, and a radius fit;
- every selected row: its fill, visible insets and a radius fit;
- the `NSBox` group boxes.

`sh tools/appkitscenes/sheets.sh CAPTURES SHEETS` composes:

- one sheet per scene: light and dark, by four phases;
- an overview;
- a sheet of 3× crops of the window-level details.

## What it cannot show

- **The user's settings decide some values.** These are the Liquid Glass slider (`NSGlassTintAmount`), the sidebar size, Reduce Transparency and Increase Contrast. The run records them and never changes them.
- **Window shadows and the desktop behind the window.** GnuiShot captures one window.
- **Hover and press states.** The tool never moves the pointer on the test Mac.
