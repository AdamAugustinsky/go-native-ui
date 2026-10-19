package icon

// The standard icons, in the order of Standard. Apps add their own after them:
// the first extra Spec passed to New has the ID NumStandard.
const (
	// ChevronDown, ChevronRight, ChevronUp and ChevronLeft point the way a
	// disclosure or a menu opens.
	ChevronDown ID = iota
	ChevronRight
	ChevronUp
	ChevronLeft
	// Check marks a checked box or a chosen item.
	Check
	// Close is a cross: close a tab, a panel or a window.
	Close
	// Minimise, Maximise and Restore are the window buttons' symbols.
	Minimise
	Maximise
	Restore
	// Search is a magnifying glass.
	Search
	// Folder and File stand for the two kinds of directory entry.
	Folder
	File
	// Plus and Minus add and remove.
	Plus
	Minus
	// Ellipsis is three dots: more actions.
	Ellipsis
	// SortUp and SortDown mark the sort direction of a column.
	SortUp
	SortDown
	// Info, Warning and Error are the symbols of the three message levels.
	Info
	Warning
	Error
	// CheckMark is the bold check of a checked checkbox, sized for its box.
	CheckMark
	// ChevronUpDown is the double chevron of a pop-up button.
	ChevronUpDown
	// ClearField is a filled circle with a cross knocked out: a search
	// field's clear button.
	ClearField
	// NumStandard is the number of standard icons.
	NumStandard
)

// Standard returns the specs of the standard icons, indexed by ID. Each names
// its SF Symbol (macOS), its codepoint in Segoe Fluent Icons and Segoe MDL2
// Assets (Windows; the two share these assignments), and a glyph of the system
// UI font for everywhere else.
func Standard() []Spec {
	s := make([]Spec, NumStandard)
	copy(s, standard[:])
	return s
}

var standard = [NumStandard]Spec{
	ChevronDown:  {Symbol: "chevron.down", Segoe: 0xE70D, Fallback: '▾', Pt: 12, Weight: 0.3},
	ChevronRight: {Symbol: "chevron.right", Segoe: 0xE76C, Fallback: '▸', Pt: 12, Weight: 0.3},
	ChevronUp:    {Symbol: "chevron.up", Segoe: 0xE70E, Fallback: '▴', Pt: 12, Weight: 0.3},
	ChevronLeft:  {Symbol: "chevron.left", Segoe: 0xE76B, Fallback: '◂', Pt: 12, Weight: 0.3},
	Check:        {Symbol: "checkmark", Segoe: 0xE73E, Fallback: '✓', Pt: 12, Weight: 0.3},
	Close:        {Symbol: "xmark", Segoe: 0xE8BB, Fallback: '✕', Pt: 11, Weight: 0.23},
	Minimise:     {Symbol: "minus", Segoe: 0xE921, Fallback: '−', Pt: 11, Weight: 0.23},
	Maximise:     {Symbol: "square", Segoe: 0xE922, Fallback: '□', Pt: 10, Weight: 0.23},
	Restore:      {Symbol: "square.on.square", Segoe: 0xE923, Fallback: '❐', Pt: 10, Weight: 0.23},
	Search:       {Symbol: "magnifyingglass", Segoe: 0xE721, Fallback: '⌕', Pt: 12.5, Weight: 0.23},
	Folder:       {Symbol: "folder.fill", Segoe: 0xE8B7, Fallback: '▣', Pt: 11.5, Weight: 0},
	File:         {Symbol: "doc", Segoe: 0xE8A5, Fallback: '▢', Pt: 11, Weight: 0},
	Plus:         {Symbol: "plus", Segoe: 0xE710, Fallback: '+', Pt: 12.5, Weight: 0.23},
	Minus:        {Symbol: "minus", Segoe: 0xE738, Fallback: '−', Pt: 12.5, Weight: 0.23},
	Ellipsis:     {Symbol: "ellipsis", Segoe: 0xE712, Fallback: '…', Pt: 12.5, Weight: 0.23},
	SortUp:       {Symbol: "arrowtriangle.up.fill", Segoe: 0xEDDB, Fallback: '▲', Pt: 8, Weight: 0},
	SortDown:     {Symbol: "arrowtriangle.down.fill", Segoe: 0xEDDC, Fallback: '▼', Pt: 8, Weight: 0},
	Info:         {Symbol: "info.circle", Segoe: 0xE946, Fallback: 'ⓘ', Pt: 12.5, Weight: 0},
	Warning:      {Symbol: "exclamationmark.triangle", Segoe: 0xE7BA, Fallback: '⚠', Pt: 12.5, Weight: 0},
	Error:        {Symbol: "xmark.octagon", Segoe: 0xE783, Fallback: '✖', Pt: 12.5, Weight: 0},
	// The controls' glyphs, sized to macOS 27.2's (tools/appkitref): the
	// check of a 16 pt checkbox, the chevrons of a 24 pt pop-up button, the
	// clear button of a 24 pt search field.
	CheckMark:     {Symbol: "checkmark", Segoe: 0xE73E, Fallback: '✓', Pt: 10, Weight: 0.56},
	ChevronUpDown: {Symbol: "chevron.up.chevron.down", Segoe: 0xE8CB, Fallback: '⇕', Pt: 9.5, Weight: 0.4},
	ClearField:    {Symbol: "xmark.circle.fill", Segoe: 0xE894, Fallback: '⊗', Pt: 13, Weight: 0},
}
