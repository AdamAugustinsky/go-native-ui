package launchguard

import (
	"errors"
	"testing"
)

func TestTestBinaryIsRecognised(t *testing.T) {
	for name, want := range map[string]bool{
		"/tmp/go-build1/b001/shell.test":                   true,
		`C:\Temp\go-build1\b001\shell.test.exe`:            true,
		"shell.test":                                       true,
		"/Applications/ZapFast.app/Contents/MacOS/ZapFast": false,
		"/usr/local/bin/gnui":                              false,
		"/tmp/latest.tests":                                false,
	} {
		if got := isTestName(name); got != want {
			t.Errorf("isTestName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestCheckRefusesInATestBinaryUnlessAllowed(t *testing.T) {
	if !testBinary {
		t.Fatal("this test binary is not recognised as one")
	}
	t.Setenv(Env, "")
	if err := Check("open URL https://example.com"); !errors.Is(err, ErrTestBinary) {
		t.Errorf("Check = %v, want ErrTestBinary", err)
	}
	t.Setenv(Env, "1")
	if err := Check("open URL https://example.com"); err != nil {
		t.Errorf("Check with %s=1 = %v, want nil", Env, err)
	}
}
