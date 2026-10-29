package install_test

import (
	"errors"
	"runtime"
	"testing"

	"gonativeui/app/install"
)

func TestOptionsChecked(t *testing.T) {
	for _, o := range []install.Options{
		{Name: "X"},
		{ID: "x"},
		{ID: "x", Name: "X", Schemes: []string{"9bad"}},
		{ID: "x", Name: "X", Schemes: []string{"a3kb:"}},
	} {
		if err := install.Install(o); !errors.Is(err, install.ErrOptions) {
			t.Errorf("Install(%+v) = %v", o, err)
		}
	}
	if err := install.RegisterScheme(""); !errors.Is(err, install.ErrOptions) {
		t.Errorf("RegisterScheme(\"\") = %v", err)
	}
}

// TestContract: ErrNotNeeded on macOS, nil on Windows, ErrUnsupported on
// Linux; a test binary is never installed.
func TestContract(t *testing.T) {
	err := install.Supported()
	switch runtime.GOOS {
	case "darwin":
		if !errors.Is(err, install.ErrNotNeeded) || errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("Supported = %v, want ErrNotNeeded", err)
		}
	default:
		if err != nil && !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("Supported = %v", err)
		}
	}
	if err := install.Install(install.Options{ID: "dev.gonativeui.test", Name: "Test", Schemes: []string{"gnui-test"}}); err == nil {
		t.Fatal("Install from a test binary succeeded")
	}
}
