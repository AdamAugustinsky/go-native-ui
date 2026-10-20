//go:build !darwin && !windows

package secret

import "gonativeui/internal/dbus"

func osBackend() backend { return secretService{dial: dbus.DialSession} }
