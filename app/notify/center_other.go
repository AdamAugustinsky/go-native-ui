//go:build !darwin

package notify

// No notification center here yet (D34: macOS first): toast notifications on
// Windows and the freedesktop.org notification service on Linux come later.

type nativeCenter struct{}

func (nativeCenter) supported() bool                    { return false }
func (nativeCenter) available() bool                    { return false }
func (nativeCenter) launched() bool                     { return false }
func (nativeCenter) start() error                       { return ErrUnsupported }
func (nativeCenter) authorize(Ticket, Permission) error { return ErrUnsupported }
func (nativeCenter) settings(Ticket) error              { return ErrUnsupported }
func (nativeCenter) categories([]Category) error        { return ErrUnsupported }
func (nativeCenter) prepare(*Note, string, uint64, string, int) (prepared, error) {
	return nil, ErrUnsupported
}
func (nativeCenter) send(prepared)       {}
func (nativeCenter) discard(prepared)    {}
func (nativeCenter) remove([]string)     {}
func (nativeCenter) pending(int) error   { return ErrUnsupported }
func (nativeCenter) delivered(int) error { return ErrUnsupported }
