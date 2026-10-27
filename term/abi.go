package term

import "unsafe"

// Go mirrors of the libghostty-vt C types term passes, at the pinned
// commit (internal/ghosttyvt). abi_test.go checks every size, field offset
// and enum value here against the loaded library's own type manifest
// (ghostty_type_json), so an API change upstream fails a test rather than
// corrupting memory. The struct mirrors hold no Go pointers: pointer fields
// are uintptr, set to C memory or to Go heap memory that the Terminal keeps
// alive.

// Result codes (GhosttyResult).
const (
	resSuccess    = 0
	resOutOfSpace = -3
	resNoValue    = -4
)

// GhosttyTerminalOption.
const (
	optUserdata        = 0
	optWritePty        = 1
	optBell            = 2
	optTitleChanged    = 5
	optColorFG         = 11
	optColorBG         = 12
	optColorCursor     = 13
	optColorPalette    = 14
	optSelection       = 21
	optClipboardWrite  = 26
	optScrollbackBytes = 27
	optScrollbackLines = 28
	optRenderHold      = 41
)

// GhosttyTerminalData.
const (
	dataCols           = 1
	dataRows           = 2
	dataActiveScreen   = 6
	dataScrollbar      = 9
	dataMouseTracking  = 11
	dataTitle          = 12
	dataScrollbackRows = 15
	dataSelection      = 31
	dataViewportActive = 32
	dataMode           = 37
	dataMemoryUsage    = 42
)

// GhosttyRenderStateData and its row and cell kinds.
const (
	rsDirty       = 3
	rsRowIterator = 4
	rsCursor      = 18
	rsColors      = 19

	rowDirty     = 1
	rowRaw       = 2
	rowCells     = 3
	rowSelection = 4
	rowCellsRaw  = 5
	rowID        = 7

	cellsStyle        = 2
	cellsGraphemesLen = 3
	cellsGraphemesBuf = 4

	rowDataWrap = 1 // GhosttyRowData
)

// Point tags, scroll tags, formats, and the encoders' options.
const (
	pointViewport = 1
	pointScreen   = 2

	scrollTop    = 0
	scrollBottom = 1
	scrollDelta  = 2
	scrollRow    = 3

	formatPlain = 0
	formatVT    = 1
	formatHTML  = 2

	mouseOptSize          = 2
	mouseOptAnyPressed    = 3
	keyOptMacOptionAsAlt  = 6
	searchOptNeedle       = 0
	searchOptSelectNext   = 1
	searchOptSelectPrev   = 2
	searchDataTotal       = 2
	searchDataSelIndex    = 3
	searchDataViewMatches = 6
)

// Cell content tags and widths (GhosttyCellContentTag, GhosttyCellWide).
const (
	contentCodepoint = 0
	contentGrapheme  = 1
	contentBGPalette = 2
	contentBGRGB     = 3

	wideNarrow     = 0
	wideWide       = 1
	wideSpacerTail = 2
	wideSpacerHead = 3
)

// GhosttyMods bits.
const (
	ghModShift = 1 << 0
	ghModCtrl  = 1 << 1
	ghModAlt   = 1 << 2
	ghModSuper = 1 << 3
)

// ghString is GhosttyString.
type ghString struct {
	ptr uintptr
	n   uint64
}

// ghRGB is GhosttyColorRgb.
type ghRGB struct{ r, g, b uint8 }

// ghStyleColor is GhosttyStyleColor: a tag (none, palette, RGB) and a value
// (the palette index or the RGB triple in its low bytes).
type ghStyleColor struct {
	tag   int32
	_     uint32
	value [8]byte
}

// ghStyle is GhosttyStyle.
type ghStyle struct {
	size                                 uint64
	fg, bg, ul                           ghStyleColor
	bold, italic, faint, blink           bool
	inverse, invisible, strike, overline bool
	underline                            int32
	_                                    uint32
}

// ghGridRef is GhosttyGridRef: a position in the terminal's page list,
// valid until the next mutating call.
type ghGridRef struct {
	size uint64
	node uintptr
	x, y uint16
	_    uint32
}

// ghSelection is GhosttySelection.
type ghSelection struct {
	size       uint64
	start, end ghGridRef
	rectangle  bool
	_          [7]byte
}

// ghPoint is GhosttyPoint with its coordinate arm.
type ghPoint struct {
	tag int32
	_   uint32
	x   uint16
	_   uint16
	y   uint32
	_   uint64
}

// ghCoord is GhosttyPointCoordinate.
type ghCoord struct {
	x uint16
	_ uint16
	y uint32
}

// ghScroll is GhosttyTerminalScrollViewport.
type ghScroll struct {
	tag   int32
	_     uint32
	value int64
	_     uint64
}

// ghScrollbar is GhosttyTerminalScrollbar.
type ghScrollbar struct{ total, offset, len uint64 }

// ghModeConfig is GhosttyTerminalModeConfig.
type ghModeConfig struct {
	mode  uint16
	value bool
	_     uint8
}

// ghCursor is GhosttyRenderStateCursor.
type ghCursor struct {
	size                     uint64
	hasViewport              bool
	_                        uint8
	x, y                     uint16
	wideTail, visible, blink bool
	password                 bool
	_                        uint16
	style                    int32
}

// ghColors is GhosttyRenderStateColors.
type ghColors struct {
	size        uint64
	bg, fg, cur ghRGB
	hasCursor   bool
	palette     [256]ghRGB
	_           [6]byte
}

// ghRowSel is GhosttyRenderStateRowSelection.
type ghRowSel struct {
	size       uint64
	start, end uint16
	_          uint32
}

// ghCells is GhosttyCellsView.
type ghCells struct {
	ptr uintptr
	n   uint64
}

// ghFormatterOpts is GhosttyFormatterTerminalOptions with its extras.
type ghFormatterOpts struct {
	size         uint64
	emit         int32
	unwrap, trim bool
	_            uint16
	extra        struct {
		size                                         uint64
		palette, modes, region, tabstops, pwd, keybd bool
		_                                            uint16
		screen                                       struct {
			size                                             uint64
			cursor, style, link, protection, kitty, charsets bool
			_                                                uint16
		}
	}
	selection uintptr
}

// ghSelFormatOpts is GhosttyTerminalSelectionFormatOptions.
type ghSelFormatOpts struct {
	size         uint64
	emit         int32
	unwrap, trim bool
	_            uint16
	selection    uintptr
}

// ghSelectWordOpts is GhosttyTerminalSelectWordOptions.
type ghSelectWordOpts struct {
	size       uint64
	ref        ghGridRef
	boundaries uintptr
	nb         uint64
}

// ghSelectLineOpts is GhosttyTerminalSelectLineOptions.
type ghSelectLineOpts struct {
	size     uint64
	ref      ghGridRef
	ws       uintptr
	nws      uint64
	semantic bool
	_        [7]byte
}

// ghMouseSize is GhosttyMouseEncoderSize.
type ghMouseSize struct {
	size                                 uint64
	screenW, screenH, cellW, cellH       uint32
	padTop, padBottom, padRight, padLeft uint32
}

// ghMemory is GhosttyTerminalMemoryUsage.
type ghMemory struct {
	size                                          uint64
	compression                                   bool
	_                                             [7]byte
	pPages, pVirtual, pResident, pCPages, pCBytes uint64
	pImages                                       uint64
	aPages, aVirtual, aResident, aCPages, aCBytes uint64
	aImages                                       uint64
}

// RowID is GhosttyRenderStateRowId: a row's identity across updates.
type RowID [2]uint64

// ghClipboardWrite and ghClipboardContent are GhosttyClipboardWrite and
// GhosttyClipboardContent, read in place in C memory by the callback.
type ghClipboardWrite struct {
	size        uint64
	location    int32
	_           uint32
	contents    uintptr
	ncontents   uint64
	name        ghString
	granted     bool
	canRemember bool
	_           [6]byte
	ctx, reply  uintptr
}

type ghClipboardContent struct{ mime, data ghString }

// ghSelBuffer is GhosttySelectionBuffer.
type ghSelBuffer struct {
	ptr      uintptr
	cap, len uint64
}

// abiStructs are the mirrors abi_test.go checks, by C name.
var abiStructs = map[string]uintptr{
	"GhosttyString":                         unsafe.Sizeof(ghString{}),
	"GhosttyStyleColor":                     unsafe.Sizeof(ghStyleColor{}),
	"GhosttyStyle":                          unsafe.Sizeof(ghStyle{}),
	"GhosttyGridRef":                        unsafe.Sizeof(ghGridRef{}),
	"GhosttySelection":                      unsafe.Sizeof(ghSelection{}),
	"GhosttyPoint":                          unsafe.Sizeof(ghPoint{}),
	"GhosttyPointCoordinate":                unsafe.Sizeof(ghCoord{}),
	"GhosttyTerminalScrollViewport":         unsafe.Sizeof(ghScroll{}),
	"GhosttyTerminalScrollbar":              unsafe.Sizeof(ghScrollbar{}),
	"GhosttyTerminalModeConfig":             unsafe.Sizeof(ghModeConfig{}),
	"GhosttyRenderStateCursor":              unsafe.Sizeof(ghCursor{}),
	"GhosttyRenderStateColors":              unsafe.Sizeof(ghColors{}),
	"GhosttyRenderStateRowSelection":        unsafe.Sizeof(ghRowSel{}),
	"GhosttyCellsView":                      unsafe.Sizeof(ghCells{}),
	"GhosttyFormatterTerminalOptions":       unsafe.Sizeof(ghFormatterOpts{}),
	"GhosttyTerminalSelectionFormatOptions": unsafe.Sizeof(ghSelFormatOpts{}),
	"GhosttyTerminalSelectWordOptions":      unsafe.Sizeof(ghSelectWordOpts{}),
	"GhosttyTerminalSelectLineOptions":      unsafe.Sizeof(ghSelectLineOpts{}),
	"GhosttyMouseEncoderSize":               unsafe.Sizeof(ghMouseSize{}),
	"GhosttyTerminalMemoryUsage":            unsafe.Sizeof(ghMemory{}),
	"GhosttyClipboardWrite":                 unsafe.Sizeof(ghClipboardWrite{}),
	"GhosttyClipboardContent":               unsafe.Sizeof(ghClipboardContent{}),
	"GhosttySelectionBuffer":                unsafe.Sizeof(ghSelBuffer{}),
	"GhosttyRenderStateRowId":               unsafe.Sizeof(RowID{}),
}
