// Package ghosttyvt pins the libghostty-vt build the framework binds (D74)
// and names what package term calls in it: the Ghostty commit, the Zig
// release that builds it, the library's file name on each system, and the C
// functions term looks up. tools/libghostty builds the libraries from this
// pin and checks that each one exports every function listed here.
package ghosttyvt

// Commit is the Ghostty commit the libraries are built from (origin/main
// on 2026-10-27). libghostty-vt's C API is marked unstable upstream: moving
// the pin means rebuilding (go run ./tools/libghostty) and running term's
// tests, which check the struct layouts and enum values against the
// library's own type manifest (ghostty_type_json).
const Commit = "4a4fc017940bec38c93b278c10c6a18c591f3e63"

// ZigVersion is the Zig release Ghostty requires at Commit
// (build.zig.zon's minimum_zig_version).
const ZigVersion = "0.17.0"

// Short is the abbreviated commit used in cache paths.
const Short = "4a4fc0179"

// FileName is the library's file name on goos: what an app bundles beside
// its executable or in Contents/Frameworks.
func FileName(goos string) string {
	switch goos {
	case "darwin":
		return "libghostty-vt.dylib"
	case "windows":
		return "ghostty-vt.dll"
	}
	return "libghostty-vt.so"
}

// The functions term calls, as indices into Names.
const (
	TypeJSON = iota
	Free
	TerminalNew
	TerminalFree
	TerminalReset
	TerminalResize
	TerminalSet
	TerminalVTWrite
	TerminalScrollViewport
	TerminalGet
	TerminalGridRef
	TerminalGridRefTrack
	TerminalPointFromGridRef
	TrackedGridRefFree
	TrackedGridRefPoint
	GridRefHyperlinkURI
	RenderStateNew
	RenderStateFree
	RenderStateUpdate
	RenderStateGet
	RenderStateClean
	RowIteratorNew
	RowIteratorFree
	RowIteratorNext
	RowGetMulti
	RenderRowGet
	RowCellsNew
	RowCellsFree
	RowCellsSelect
	RowCellsGet
	RowGet
	FormatterTerminalNew
	FormatterFormatBuf
	FormatterFree
	SelectWord
	SelectLine
	SelectAll
	SelectionFormatBuf
	KeyEncoderNew
	KeyEncoderFree
	KeyEncoderSetoptFromTerminal
	KeyEncoderSetopt
	KeyEncoderEncode
	KeyEventNew
	KeyEventFree
	KeyEventSetAction
	KeyEventSetKey
	KeyEventSetMods
	KeyEventSetUTF8
	KeyEventSetUnshiftedCodepoint
	KeyEventSetComposing
	MouseEncoderNew
	MouseEncoderFree
	MouseEncoderSetopt
	MouseEncoderSetoptFromTerminal
	MouseEncoderEncode
	MouseEventNew
	MouseEventFree
	MouseEventSetAction
	MouseEventSetButton
	MouseEventClearButton
	MouseEventSetMods
	MouseEventSetPosition
	FocusEncode
	PasteEncode
	PasteIsSafe
	SearchNew
	SearchFree
	SearchSet
	SearchGet
	SearchRun
	SearchFeed
	NumFuncs
)

// Names are the C names of the functions, indexed by the constants above.
var Names = [NumFuncs]string{
	TypeJSON:                       "ghostty_type_json",
	Free:                           "ghostty_free",
	TerminalNew:                    "ghostty_terminal_new",
	TerminalFree:                   "ghostty_terminal_free",
	TerminalReset:                  "ghostty_terminal_reset",
	TerminalResize:                 "ghostty_terminal_resize",
	TerminalSet:                    "ghostty_terminal_set",
	TerminalVTWrite:                "ghostty_terminal_vt_write",
	TerminalScrollViewport:         "ghostty_terminal_scroll_viewport",
	TerminalGet:                    "ghostty_terminal_get",
	TerminalGridRef:                "ghostty_terminal_grid_ref",
	TerminalGridRefTrack:           "ghostty_terminal_grid_ref_track",
	TerminalPointFromGridRef:       "ghostty_terminal_point_from_grid_ref",
	TrackedGridRefFree:             "ghostty_tracked_grid_ref_free",
	TrackedGridRefPoint:            "ghostty_tracked_grid_ref_point",
	GridRefHyperlinkURI:            "ghostty_grid_ref_hyperlink_uri",
	RenderStateNew:                 "ghostty_render_state_new",
	RenderStateFree:                "ghostty_render_state_free",
	RenderStateUpdate:              "ghostty_render_state_update",
	RenderStateGet:                 "ghostty_render_state_get",
	RenderStateClean:               "ghostty_render_state_clean",
	RowIteratorNew:                 "ghostty_render_state_row_iterator_new",
	RowIteratorFree:                "ghostty_render_state_row_iterator_free",
	RowIteratorNext:                "ghostty_render_state_row_iterator_next",
	RowGetMulti:                    "ghostty_render_state_row_get_multi",
	RenderRowGet:                   "ghostty_render_state_row_get",
	RowCellsNew:                    "ghostty_render_state_row_cells_new",
	RowCellsFree:                   "ghostty_render_state_row_cells_free",
	RowCellsSelect:                 "ghostty_render_state_row_cells_select",
	RowCellsGet:                    "ghostty_render_state_row_cells_get",
	RowGet:                         "ghostty_row_get",
	FormatterTerminalNew:           "ghostty_formatter_terminal_new",
	FormatterFormatBuf:             "ghostty_formatter_format_buf",
	FormatterFree:                  "ghostty_formatter_free",
	SelectWord:                     "ghostty_terminal_select_word",
	SelectLine:                     "ghostty_terminal_select_line",
	SelectAll:                      "ghostty_terminal_select_all",
	SelectionFormatBuf:             "ghostty_terminal_selection_format_buf",
	KeyEncoderNew:                  "ghostty_key_encoder_new",
	KeyEncoderFree:                 "ghostty_key_encoder_free",
	KeyEncoderSetoptFromTerminal:   "ghostty_key_encoder_setopt_from_terminal",
	KeyEncoderSetopt:               "ghostty_key_encoder_setopt",
	KeyEncoderEncode:               "ghostty_key_encoder_encode",
	KeyEventNew:                    "ghostty_key_event_new",
	KeyEventFree:                   "ghostty_key_event_free",
	KeyEventSetAction:              "ghostty_key_event_set_action",
	KeyEventSetKey:                 "ghostty_key_event_set_key",
	KeyEventSetMods:                "ghostty_key_event_set_mods",
	KeyEventSetUTF8:                "ghostty_key_event_set_utf8",
	KeyEventSetUnshiftedCodepoint:  "ghostty_key_event_set_unshifted_codepoint",
	KeyEventSetComposing:           "ghostty_key_event_set_composing",
	MouseEncoderNew:                "ghostty_mouse_encoder_new",
	MouseEncoderFree:               "ghostty_mouse_encoder_free",
	MouseEncoderSetopt:             "ghostty_mouse_encoder_setopt",
	MouseEncoderSetoptFromTerminal: "ghostty_mouse_encoder_setopt_from_terminal",
	MouseEncoderEncode:             "ghostty_mouse_encoder_encode",
	MouseEventNew:                  "ghostty_mouse_event_new",
	MouseEventFree:                 "ghostty_mouse_event_free",
	MouseEventSetAction:            "ghostty_mouse_event_set_action",
	MouseEventSetButton:            "ghostty_mouse_event_set_button",
	MouseEventClearButton:          "ghostty_mouse_event_clear_button",
	MouseEventSetMods:              "ghostty_mouse_event_set_mods",
	MouseEventSetPosition:          "ghostty_mouse_event_set_position",
	FocusEncode:                    "ghostty_focus_encode",
	PasteEncode:                    "ghostty_paste_encode",
	PasteIsSafe:                    "ghostty_paste_is_safe",
	SearchNew:                      "ghostty_search_new",
	SearchFree:                     "ghostty_search_free",
	SearchSet:                      "ghostty_search_set",
	SearchGet:                      "ghostty_search_get",
	SearchRun:                      "ghostty_search_run",
	SearchFeed:                     "ghostty_search_feed",
}
