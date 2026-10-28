//go:build !darwin && !windows

package login

func osStatusItem(Item) (State, error)  { return NotFound, ErrUnsupported }
func osEnableItem(Item) (State, error)  { return NotFound, ErrUnsupported }
func osDisableItem(Item) (State, error) { return NotFound, ErrUnsupported }
func osOpenSettings() error             { return ErrUnsupported }
