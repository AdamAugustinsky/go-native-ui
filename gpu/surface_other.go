//go:build !darwin && !windows

package gpu

import (
	"errors"
	"fmt"
	"runtime"

	"gonativeui/platform"
)

// Device is the GPU device an app's windows share. Only macOS and Windows
// have more than one window so far (D30, D68): on Linux the window's surface
// makes its own device (NewSurface), and NewDevice reports ErrUnsupported.
type Device struct{}

var errNoSharedDevice = fmt.Errorf("gpu: a device shared by windows on %s: %w", runtime.GOOS, errors.ErrUnsupported)

// NewDevice reports ErrUnsupported here: see Device.
func NewDevice() (*Device, error) { return nil, errNoSharedDevice }

// Handles returns no handles here.
func (d *Device) Handles() (device, queue uintptr) { return 0, 0 }

// Release does nothing here.
func (d *Device) Release() {}

// NewSurfaceOn reports ErrUnsupported here: see Device.
func NewSurfaceOn(d *Device, n platform.Native, w, h int32, scale float32) (*Surface, error) {
	return nil, errNoSharedDevice
}
