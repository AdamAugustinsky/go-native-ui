//go:build !darwin

package main

import "errors"

func runProbe() error { return errors.New("-probe-titlebar is only available on macOS") }
