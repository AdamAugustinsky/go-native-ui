//go:build !darwin

package login

func osStatus(string) (State, error)  { return NotFound, ErrUnsupported }
func osEnable(string) (State, error)  { return NotFound, ErrUnsupported }
func osDisable(string) (State, error) { return NotFound, ErrUnsupported }
func osOpenSettings() error           { return ErrUnsupported }
