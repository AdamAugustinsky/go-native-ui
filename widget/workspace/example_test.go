package workspace_test

import (
	"fmt"

	"gonativeui/widget/workspace"
)

// Zap's panes: a list with a fixed, clamped width before the chat, and a side
// pane beside the chat that shows only while it has room. Closing the chat
// leaves its pane on screen, empty, and the focus moves to the list.
func Example() {
	const list, chat, side workspace.PaneID = 1, 2, 3
	var l workspace.Layout
	l.Reset(chat)
	l.Split(chat, list, workspace.Left, 0.3)
	l.SetLimits(list, workspace.Limits{Min: [2]float32{260}, Max: [2]float32{520}})
	l.SetFixed(list, 300)
	l.Split(chat, side, workspace.Right, 0.3)
	l.SetLimits(chat, workspace.Limits{Min: [2]float32{320}})
	l.SetLimits(side, workspace.Limits{Min: [2]float32{240}, Max: [2]float32{640}, Yield: true})

	m := workspace.Metrics{Scale: 1, Gutter: [2]float32{8, 8}, Min: [2]float32{100, 100}}
	l.Arrange([4]float32{0, 0, 1200, 800}, m)
	fmt.Println("wide: side shown:", l.Shown(side))
	l.Arrange([4]float32{0, 0, 800, 800}, m)
	fmt.Println("narrow: side shown:", l.Shown(side))

	l.SetFocus(chat)
	l.SetEmpty(chat, true) // the chat was closed
	fmt.Println("focus on the list:", l.Focus() == list)
	// Output:
	// wide: side shown: true
	// narrow: side shown: false
	// focus on the list: true
}
