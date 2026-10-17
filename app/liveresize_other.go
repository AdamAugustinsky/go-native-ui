//go:build !darwin

package app

import "errors"

// RunLiveResizeTest is only implemented on macOS (platform.SimulateLiveResize).
func RunLiveResizeTest(Config, Builder, [][2]float64) (LiveResizeReport, error) {
	return LiveResizeReport{}, errors.New("app: the live-resize test is only available on macOS")
}
