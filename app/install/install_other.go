//go:build !darwin && !windows

package install

// Linux waits (D63).

func osSupported() error            { return ErrUnsupported }
func osDefaults(o Options) Options  { return o }
func osInstalled() (string, bool)   { return "", false }
func osInstall(Options) error       { return ErrUnsupported }
func osUninstall(Options) error     { return ErrUnsupported }
func osRegisterScheme(string) error { return ErrUnsupported }
