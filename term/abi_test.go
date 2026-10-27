package term

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"

	"gonativeui/internal/ghosttyvt"
)

// TestMain points term at tools/libghostty's cache when GNUI_LIBGHOSTTY_VT
// is unset, so `go test ./term/...` finds the library after
// `go run ./tools/libghostty`.
func TestMain(m *testing.M) {
	if os.Getenv(EnvLibrary) == "" {
		if d, err := os.Getwd(); err == nil {
			for ; d != filepath.Dir(d); d = filepath.Dir(d) {
				if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
					os.Setenv(EnvLibrary, filepath.Join(d, ".cache", "libghostty-vt", ghosttyvt.Short, runtime.GOOS+"-"+runtime.GOARCH))
					break
				}
			}
		}
	}
	os.Exit(m.Run())
}

// needLib skips a test when the library is missing, unless
// GNUI_REQUIRE_LIBGHOSTTY is set (CI sets it where it built the library).
func needLib(t testing.TB) {
	t.Helper()
	if err := Available(); err != nil {
		if os.Getenv("GNUI_REQUIRE_LIBGHOSTTY") != "" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
}

// TestABI checks the mirrored structs (sizes and the offsets term relies
// on) and the enum values against the loaded library's type manifest.
func TestABI(t *testing.T) {
	needLib(t)
	m, err := manifest()
	if err != nil {
		t.Fatal(err)
	}
	for name, size := range abiStructs {
		d, err := m.desc(name)
		if err != nil {
			t.Error(err)
			continue
		}
		if d.Size != size {
			t.Errorf("%s: %d bytes in the library, %d in term", name, d.Size, size)
		}
	}
	var (
		st  ghStyle
		cur ghCursor
		sel ghSelection
		pt  ghPoint
		fo  ghFormatterOpts
		cw  ghClipboardWrite
		ms  ghMouseSize
		mem ghMemory
		rs  ghRowSel
		lo  ghSelectLineOpts
		co  ghColors
		cd  ghCoord
	)
	offsets := []struct {
		typ, field string
		off        uintptr
	}{
		{"GhosttyStyle", "fg_color", unsafe.Offsetof(st.fg)}, {"GhosttyStyle", "bold", unsafe.Offsetof(st.bold)},
		{"GhosttyStyle", "overline", unsafe.Offsetof(st.overline)}, {"GhosttyStyle", "underline", unsafe.Offsetof(st.underline)},
		{"GhosttyRenderStateCursor", "viewport_x", unsafe.Offsetof(cur.x)}, {"GhosttyRenderStateCursor", "viewport_y", unsafe.Offsetof(cur.y)},
		{"GhosttyRenderStateCursor", "visible", unsafe.Offsetof(cur.visible)}, {"GhosttyRenderStateCursor", "visual_style", unsafe.Offsetof(cur.style)},
		{"GhosttySelection", "end", unsafe.Offsetof(sel.end)}, {"GhosttySelection", "rectangle", unsafe.Offsetof(sel.rectangle)},
		{"GhosttyPoint", "value", unsafe.Offsetof(pt.x)},
		{"GhosttyPointCoordinate", "y", unsafe.Offsetof(cd.y)},
		{"GhosttyFormatterTerminalOptions", "extra", unsafe.Offsetof(fo.extra)}, {"GhosttyFormatterTerminalOptions", "selection", unsafe.Offsetof(fo.selection)},
		{"GhosttyFormatterTerminalExtra", "screen", unsafe.Offsetof(fo.extra.screen)},
		{"GhosttyClipboardWrite", "contents", unsafe.Offsetof(cw.contents)}, {"GhosttyClipboardWrite", "contents_len", unsafe.Offsetof(cw.ncontents)},
		{"GhosttyMouseEncoderSize", "cell_width", unsafe.Offsetof(ms.cellW)}, {"GhosttyMouseEncoderSize", "padding_left", unsafe.Offsetof(ms.padLeft)},
		{"GhosttyTerminalMemoryUsage", "primary_resident_bytes", unsafe.Offsetof(mem.pResident)},
		{"GhosttyTerminalMemoryUsage", "alternate_resident_bytes", unsafe.Offsetof(mem.aResident)},
		{"GhosttyTerminalMemoryUsage", "primary_pages", unsafe.Offsetof(mem.pPages)},
		{"GhosttyRenderStateRowSelection", "end_x", unsafe.Offsetof(rs.end)},
		{"GhosttyTerminalSelectLineOptions", "semantic_prompt_boundary", unsafe.Offsetof(lo.semantic)},
		{"GhosttyRenderStateColors", "palette", unsafe.Offsetof(co.palette)},
	}
	for _, o := range offsets {
		d, err := m.desc(o.typ)
		if err != nil {
			t.Error(err)
			continue
		}
		var f struct {
			Offset uintptr `json:"offset"`
		}
		raw, ok := d.Fields[o.field]
		if !ok {
			t.Errorf("%s has no field %s", o.typ, o.field)
			continue
		}
		json.Unmarshal(raw, &f)
		if f.Offset != o.off {
			t.Errorf("%s.%s at %d in the library, %d in term", o.typ, o.field, f.Offset, o.off)
		}
	}
	enums := []struct {
		typ, name string
		v         int64
	}{
		{"GhosttyResult", "OUT_OF_SPACE", resOutOfSpace}, {"GhosttyResult", "NO_VALUE", resNoValue},
		{"GhosttyTerminalOption", "USERDATA", optUserdata}, {"GhosttyTerminalOption", "WRITE_PTY", optWritePty},
		{"GhosttyTerminalOption", "BELL", optBell}, {"GhosttyTerminalOption", "TITLE_CHANGED", optTitleChanged},
		{"GhosttyTerminalOption", "COLOR_FOREGROUND", optColorFG}, {"GhosttyTerminalOption", "COLOR_BACKGROUND", optColorBG},
		{"GhosttyTerminalOption", "COLOR_CURSOR", optColorCursor}, {"GhosttyTerminalOption", "COLOR_PALETTE", optColorPalette},
		{"GhosttyTerminalOption", "SELECTION", optSelection}, {"GhosttyTerminalOption", "CLIPBOARD_WRITE", optClipboardWrite},
		{"GhosttyTerminalOption", "SCROLLBACK_MAX_BYTES", optScrollbackBytes}, {"GhosttyTerminalOption", "SCROLLBACK_MAX_LINES", optScrollbackLines},
		{"GhosttyTerminalOption", "RENDER_HOLD", optRenderHold},
		{"GhosttyTerminalData", "COLS", dataCols}, {"GhosttyTerminalData", "ROWS", dataRows},
		{"GhosttyTerminalData", "ACTIVE_SCREEN", dataActiveScreen}, {"GhosttyTerminalData", "SCROLLBAR", dataScrollbar},
		{"GhosttyTerminalData", "MOUSE_TRACKING", dataMouseTracking}, {"GhosttyTerminalData", "TITLE", dataTitle},
		{"GhosttyTerminalData", "SCROLLBACK_ROWS", dataScrollbackRows}, {"GhosttyTerminalData", "SELECTION", dataSelection},
		{"GhosttyTerminalData", "VIEWPORT_ACTIVE", dataViewportActive}, {"GhosttyTerminalData", "MODE", dataMode},
		{"GhosttyTerminalData", "MEMORY_USAGE", dataMemoryUsage},
		{"GhosttyRenderStateData", "DIRTY", rsDirty}, {"GhosttyRenderStateData", "ROW_ITERATOR", rsRowIterator},
		{"GhosttyRenderStateData", "CURSOR", rsCursor}, {"GhosttyRenderStateData", "COLORS", rsColors},
		{"GhosttyRenderStateRowData", "DIRTY", rowDirty}, {"GhosttyRenderStateRowData", "RAW", rowRaw},
		{"GhosttyRenderStateRowData", "CELLS", rowCells}, {"GhosttyRenderStateRowData", "SELECTION", rowSelection},
		{"GhosttyRenderStateRowData", "CELLS_RAW", rowCellsRaw},
		{"GhosttyRenderStateRowCellsData", "STYLE", cellsStyle}, {"GhosttyRenderStateRowCellsData", "GRAPHEMES_LEN", cellsGraphemesLen},
		{"GhosttyRenderStateRowCellsData", "GRAPHEMES_BUF", cellsGraphemesBuf}, {"GhosttyRowData", "WRAP", rowDataWrap},
		{"GhosttyPointTag", "VIEWPORT", pointViewport}, {"GhosttyPointTag", "SCREEN", pointScreen},
		{"GhosttyTerminalScrollViewportTag", "TOP", scrollTop}, {"GhosttyTerminalScrollViewportTag", "BOTTOM", scrollBottom},
		{"GhosttyTerminalScrollViewportTag", "DELTA", scrollDelta}, {"GhosttyTerminalScrollViewportTag", "ROW", scrollRow},
		{"GhosttyFormatterFormat", "PLAIN", formatPlain}, {"GhosttyFormatterFormat", "VT", formatVT}, {"GhosttyFormatterFormat", "HTML", formatHTML},
		{"GhosttyMouseEncoderOption", "SIZE", mouseOptSize}, {"GhosttyMouseEncoderOption", "ANY_BUTTON_PRESSED", mouseOptAnyPressed},
		{"GhosttyKeyEncoderOption", "MACOS_OPTION_AS_ALT", keyOptMacOptionAsAlt},
		{"GhosttySearchOption", "NEEDLE", searchOptNeedle}, {"GhosttySearchOption", "SELECT_NEXT", searchOptSelectNext},
		{"GhosttySearchOption", "SELECT_PREV", searchOptSelectPrev}, {"GhosttySearchData", "TOTAL_MATCHES", searchDataTotal},
		{"GhosttySearchData", "SELECTED_INDEX", searchDataSelIndex}, {"GhosttySearchData", "VIEWPORT_MATCHES", searchDataViewMatches},
		{"GhosttyCellContentTag", "CODEPOINT", contentCodepoint}, {"GhosttyCellContentTag", "CODEPOINT_GRAPHEME", contentGrapheme},
		{"GhosttyCellContentTag", "BG_COLOR_PALETTE", contentBGPalette}, {"GhosttyCellContentTag", "BG_COLOR_RGB", contentBGRGB},
		{"GhosttyCellWide", "WIDE", wideWide}, {"GhosttyCellWide", "SPACER_TAIL", wideSpacerTail}, {"GhosttyCellWide", "SPACER_HEAD", wideSpacerHead},
		{"GhosttySgrUnderline", "DASHED", int64(UnderlineDashed)}, {"GhosttySgrUnderline", "CURLY", int64(UnderlineCurly)},
		{"GhosttyKey", "ARROW_UP", int64(ghosttyKeys[KeyUp])}, {"GhosttyKey", "ENTER", int64(ghosttyKeys[KeyEnter])},
		{"GhosttyKey", "F12", int64(ghosttyKeys[KeyF12])}, {"GhosttyKey", "ESCAPE", int64(ghosttyKeys[KeyEscape])},
		{"GhosttyKey", "PAGE_DOWN", int64(ghosttyKeys[KeyPageDown])}, {"GhosttyKey", "SPACE", int64(ghosttyKeys[KeySpace])},
		{"GhosttyKey", "A", int64(runeKey('a'))}, {"GhosttyKey", "Z", int64(runeKey('z'))}, {"GhosttyKey", "DIGIT_0", int64(runeKey('0'))},
		{"GhosttyKey", "SLASH", int64(runeKey('/'))}, {"GhosttyKey", "BACKQUOTE", int64(runeKey('`'))},
		{"GhosttyMouseButton", "LEFT", 1}, {"GhosttyMouseButton", "RIGHT", 2}, {"GhosttyMouseButton", "MIDDLE", 3},
		{"GhosttyMouseAction", "PRESS", int64(MousePressed)}, {"GhosttyMouseAction", "RELEASE", int64(MouseReleased)},
		{"GhosttyMouseAction", "MOTION", int64(MouseMoved)},
	}
	for _, e := range enums {
		d, err := m.desc(e.typ)
		if err != nil {
			t.Error(err)
			continue
		}
		if v, ok := d.Values[e.name]; !ok || v != e.v {
			t.Errorf("%s %s = %d (present %v) in the library, %d in term", e.typ, e.name, v, ok, e.v)
		}
	}
	if lib.cell.cp.width != 21 || lib.cell.style.width != 16 {
		t.Errorf("cell layout %+v", lib.cell)
	}
}
