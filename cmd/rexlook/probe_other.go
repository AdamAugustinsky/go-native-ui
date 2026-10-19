//go:build !darwin

package main

import (
	"errors"

	"gonativeui/theme"
)

func runProbe() error { return errors.New("-probe-titlebar is only available on macOS") }

func runPose(theme.Preference, string, int) error {
	return errors.New("-pose is only available on macOS")
}
