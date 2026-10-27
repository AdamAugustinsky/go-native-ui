//go:build !darwin

package install

// Not yet here: W10 brings the Windows installer; Linux waits (D63). When
// W10 lands this file becomes //go:build !darwin && !windows.

func osSupported() error            { return ErrUnsupported }
func osInstalled() (string, bool)   { return "", false }
func osInstall(Options) error       { return ErrUnsupported }
func osUninstall(Options) error     { return ErrUnsupported }
func osRegisterScheme(string) error { return ErrUnsupported }
