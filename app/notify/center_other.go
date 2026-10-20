//go:build !darwin

package notify

// No notification center here yet (D34: macOS first): toast notifications on
// Windows and the freedesktop.org notification service on Linux come later.

type nativeCenter struct{}

func (nativeCenter) supported() bool                      { return false }
func (nativeCenter) available() bool                      { return false }
func (nativeCenter) start() error                         { return ErrUnsupported }
func (nativeCenter) authorize(Ticket, Permission) error   { return ErrUnsupported }
func (nativeCenter) settings(Ticket) error                { return ErrUnsupported }
func (nativeCenter) categories([]Category) error          { return ErrUnsupported }
func (nativeCenter) add(*Note, string, uint64, int) error { return ErrUnsupported }
func (nativeCenter) remove([]string)                      {}
func (nativeCenter) delivered(int) error                  { return ErrUnsupported }
