package inspectwire

import "encoding/json"

// MaxLine is the longest request, newline included: 64 KiB.
const MaxLine = 64 << 10

// The operations of a Request.
const (
	// OpWindows lists the app's windows.
	OpWindows = "windows"
	// OpTree returns the semantic tree of one window, or of every window.
	OpTree = "tree"
	// OpDo plays one step or a list of steps in a window, and answers once
	// they have been applied and the next frame has been built.
	OpDo = "do"
	// OpShot renders a window's current frame to a PNG file.
	OpShot = "shot"
)

// The actions of a Step.
const (
	// ActClick is a click of the left button on the target's centre.
	ActClick = "click"
	// ActDoubleClick is a double click.
	ActDoubleClick = "double_click"
	// ActRightClick is a click of the right button.
	ActRightClick = "right_click"
	// ActMove moves the pointer to the target's centre.
	ActMove = "move"
	// ActFocus gives the target the keyboard focus, without a click.
	ActFocus = "focus"
	// ActType types Text into the focused text field.
	ActType = "type"
	// ActKey presses Key (with modifiers, "cmd+shift+n") N times (default 1).
	ActKey = "key"
	// ActScroll scrolls over the target: Dy pixels, N times (default 1);
	// a positive Dy scrolls toward the start of the content.
	ActScroll = "scroll"
	// ActWaitFor waits until the target exists.
	ActWaitFor = "wait_for"
	// ActWaitGone waits until the target no longer exists.
	ActWaitGone = "wait_gone"
	// ActSettle waits until nothing animates.
	ActSettle = "settle"
)

// The error codes of a response.
const (
	// ErrBadRequest: the line is not a request this version understands (not
	// JSON, an unknown field, a missing or wrong value).
	ErrBadRequest = "bad_request"
	// ErrTooLarge: the request is longer than MaxLine.
	ErrTooLarge = "too_large"
	// ErrUnknownOp: Op is not one of the operations.
	ErrUnknownOp = "unknown_op"
	// ErrNoWindow: there is no such window (or none is open).
	ErrNoWindow = "no_window"
	// ErrStepFailed: a step could not do what it says (no such node, a wait
	// that ran out); the message says which and why.
	ErrStepFailed = "step_failed"
	// ErrBusy: the window is already playing a "do" (or a benchmark's script).
	ErrBusy = "busy"
	// ErrTimeout: the app did not answer in time; a hidden or covered window
	// draws no frames, which a "do" needs.
	ErrTimeout = "timeout"
	// ErrBadPath: the path of a shot is relative, inside the socket's
	// directory, or not writable.
	ErrBadPath = "bad_path"
	// ErrShotFailed: the frame could not be rendered or written.
	ErrShotFailed = "shot_failed"
	// ErrShuttingDown: the app is ending.
	ErrShuttingDown = "shutting_down"
	// ErrInternal: a bug in the bridge; the message has the panic.
	ErrInternal = "internal"
)

// Step is one thing a person does, found by role and name in the semantic
// tree. Which fields a step reads depends on its Action.
type Step struct {
	// Action is one of the Act constants.
	Action string `json:"action,omitempty"`
	// Role is the target's role as the tree names it ("Button", "TextField"),
	// compared without regard to case. Name is its name; empty matches any.
	// Nth picks the n-th match (from 0) in the tree's order.
	Role string `json:"role,omitempty"`
	// Name is the target's accessible name.
	Name string `json:"name,omitempty"`
	// Nth is which of the nodes with the role and name (from 0).
	Nth int `json:"nth,omitempty"`
	// Text is what ActType types.
	Text string `json:"text,omitempty"`
	// Key is the key of ActKey: a letter, digit or punctuation character, or
	// a name (enter, escape, tab, space, backspace, delete, left, right, up,
	// down, home, end, pageup, pagedown, f1 to f20), after any modifiers
	// joined by plus signs: shift, ctrl, alt (option), cmd (super) and primary
	// (cmd on macOS, ctrl elsewhere).
	Key string `json:"key,omitempty"`
	// Dy is the scroll of ActScroll, in pixels.
	Dy float32 `json:"dy,omitempty"`
	// N is how many times ActKey and ActScroll repeat.
	N int `json:"n,omitempty"`
	// Ms bounds a wait (ActWaitFor, ActWaitGone, ActSettle) in milliseconds
	// of frame time; 0 is 4000.
	Ms int64 `json:"ms,omitempty"`
}

// Request is one line a client sends.
type Request struct {
	// ID is echoed in the response; clients use it to match them.
	ID string `json:"id,omitempty"`
	// Op is one of the Op constants.
	Op string `json:"op"`
	// Window is the window's id (from OpWindows). Omitted, OpTree answers for
	// every window and the others use the first.
	Window uint32 `json:"window,omitempty"`
	// Path is the file OpShot writes: absolute, and not inside the socket's
	// directory.
	Path string `json:"path,omitempty"`
	// TimeoutMs bounds an OpDo (default 30000, at most 120000).
	TimeoutMs int `json:"timeout_ms,omitempty"`
	// Step is the single step of an OpDo written inline, or, for OpTree, its
	// Role and Name filter the nodes returned (their ancestors are not
	// returned; Parent still gives their ids).
	Step
	// Steps are the steps of an OpDo, played in order, instead of Step.
	Steps []Step `json:"steps,omitempty"`
}

// Error is the failure of a response.
type Error struct {
	// Code is one of the Err constants.
	Code string `json:"code"`
	// Message says what went wrong.
	Message string `json:"message"`
	// Step is the number (from 0) of the step of a "do" that failed.
	Step *int `json:"step,omitempty"`
}

// Error implements error.
func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Window describes one window.
type Window struct {
	// ID is the window's id; Title its title.
	ID    uint32 `json:"id"`
	Title string `json:"title"`
	// Width and Height are the window's content size in pixels, Scale the
	// pixels per point.
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Scale  float32 `json:"scale"`
	// Frame is the number of frames the window has drawn.
	Frame int `json:"frame"`
}

// Node is one node of a window's semantic tree.
type Node struct {
	// ID is the node's index in the tree, valid in this response only;
	// Parent is its parent's, -1 for the root.
	ID     int `json:"id"`
	Parent int `json:"parent"`
	// Role is the role's name; Name and Value the accessible name and value
	// (a secure text field's value is never the text).
	Role  string `json:"role"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
	// States are focused, selected, checked, disabled and the others the tree
	// has.
	States []string `json:"states,omitempty"`
	// Bounds are x0, y0, x1, y1 in the window's pixels.
	Bounds [4]float32 `json:"bounds"`
	// Range is min, max, value and step of a slider, scroll bar or progress
	// bar; absent for other nodes.
	Range *[4]float64 `json:"range,omitempty"`
	// Pos is the node's index in its list and the list's length, if known.
	Pos *[2]int32 `json:"pos,omitempty"`
	// Key identifies the node's box across frames and responses.
	Key string `json:"key"`
}

// Tree is the semantic tree of one window.
type Tree struct {
	// Window is the window's id; Width, Height and Scale as in Window.
	Window uint32  `json:"window"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Scale  float32 `json:"scale"`
	// Nodes are in build order, parents first.
	Nodes []Node `json:"nodes"`
	// Dropped is the number of nodes the tree was too large to hold.
	Dropped int `json:"dropped,omitempty"`
}

// Shot is the answer to OpShot.
type Shot struct {
	// Path is the file written; Width and Height its size in pixels.
	Path   string `json:"path"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	// Scale is the window's pixels per point.
	Scale float32 `json:"scale"`
}

// Response is one line the app sends back.
type Response struct {
	// ID is the request's.
	ID string `json:"id,omitempty"`
	// OK says whether the request was carried out; if not, Error says why.
	OK    bool   `json:"ok"`
	Error *Error `json:"error,omitempty"`
	// Windows answers OpWindows.
	Windows []Window `json:"windows,omitempty"`
	// Trees answers OpTree.
	Trees []Tree `json:"trees,omitempty"`
	// Steps is the number of steps an OpDo applied.
	Steps int `json:"steps,omitempty"`
	// Frames is the number of frames an OpDo took.
	Frames int `json:"frames,omitempty"`
	// Shot answers OpShot.
	Shot *Shot `json:"shot,omitempty"`
}

// Fail returns the failed response for request id.
func Fail(id, code, msg string) *Response {
	return &Response{ID: id, Error: &Error{Code: code, Message: msg}}
}

// Marshal returns the response as one line, newline included.
func (r *Response) Marshal() []byte {
	b, err := json.Marshal(r)
	if err != nil { // a value that cannot be encoded: say so instead of nothing
		b, _ = json.Marshal(Fail(r.ID, ErrInternal, err.Error()))
	}
	return append(b, '\n')
}
