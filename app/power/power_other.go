//go:build !darwin

package power

func osHold(Kind, string) (uint32, error) { return 0, ErrUnsupported }
func osRelease(uint32) error              { return nil }
func osObserve() error                    { return ErrUnsupported }
