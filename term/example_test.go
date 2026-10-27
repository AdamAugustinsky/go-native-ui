package term_test

import (
	"fmt"

	"gonativeui/term"
)

// Example feeds a program's output into a terminal, reads the screen back
// through the render state, and encodes a key press to send back.
func Example() {
	t, err := term.New(term.Options{Cols: 40, Rows: 3})
	if err != nil {
		fmt.Println("no libghostty-vt:", err != nil)
		return
	}
	defer t.Close()
	t.Write([]byte("\x1b[1;32mok\x1b[0m\r\n")) // from the agent's WebSocket, in any chunks
	t.Update()
	var row term.Row
	for t.NextRow(&row) {
		t.DecodeRow(&row)
		for _, c := range row.Cells {
			_ = t.Style(c.Style) // colours and attributes to draw it with
		}
	}
	t.CleanFrame()
	out := t.EncodeKey(nil, term.KeyEvent{Key: term.KeyUp})
	out = t.TakeReplies(out)
	_ = out // to the agent
}
