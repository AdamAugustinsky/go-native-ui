//go:build !darwin

package selfupdate

// Supported reports why this copy of the app cannot update itself: on this
// system, always ErrUnsupported.
func Supported() error { return ErrUnsupported }

// Prepare returns ErrUnsupported on this system.
func Prepare(archive string) (*Staged, error) { return nil, ErrUnsupported }

// InstallAndRelaunch returns ErrUnsupported on this system.
func (s *Staged) InstallAndRelaunch(args []string) error { return ErrUnsupported }

// Discard does nothing on this system.
func (s *Staged) Discard() error { return nil }

// RunHelperIfAsked returns at once on this system.
func RunHelperIfAsked() {}

// Started does nothing on this system.
func Started() {}

// LastResult reports None on this system.
func LastResult() Result { return Result{} }
