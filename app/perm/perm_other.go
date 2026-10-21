//go:build !darwin

package perm

func osStatus(Kind) (State, error) { return NotDetermined, ErrUnsupported }

func osPrepare(Kind) error { return ErrUnsupported }

func osRequest(Ticket, Kind) error { return ErrUnsupported }

func osOpenSettings(Kind) error { return ErrUnsupported }
