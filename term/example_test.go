package term_test

import (
	"gonativeui/term"
)

// Example feeds a program's output into a terminal and encodes a key press
// to send back.
func Example() {
	t := term.New(term.Options{Cols: 120, Rows: 40})
	t.Write([]byte("\x1b[1;32mok\x1b[0m\r\n")) // from the agent's WebSocket, in any chunks
	_, rows := t.Size()
	for y := -t.Scrollback(); y < rows; y++ {
		for _, c := range t.Row(y) {
			_ = t.Style(c.Style) // colours and attributes to draw it with
		}
	}
	var out []byte
	out = t.EncodeKey(out[:0], term.KeyUp, 0, 0)
	out = t.TakeReplies(out)
	_ = out // to the agent
}
