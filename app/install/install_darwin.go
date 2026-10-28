package install

// macOS installs a bundle by copying it; its URL schemes come from the
// bundle's Info.plist (gnui.json's urlSchemes).

func osSupported() error            { return ErrNotNeeded }
func osInstalled() (string, bool)   { return "", false }
func osInstall(Options) error       { return ErrNotNeeded }
func osUninstall(Options) error     { return ErrNotNeeded }
func osRegisterScheme(string) error { return ErrNotNeeded }
