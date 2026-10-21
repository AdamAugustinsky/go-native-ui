package login_test

import (
	"errors"
	"os"
	"testing"

	"gonativeui/app/login"
)

func TestStateStrings(t *testing.T) {
	for st, want := range map[login.State]string{
		login.NotRegistered: "not registered", login.Enabled: "enabled",
		login.RequiresApproval: "requires approval", login.NotFound: "not found",
	} {
		if got := st.String(); got != want {
			t.Errorf("State(%d) = %q, want %q", st, got, want)
		}
	}
}

func TestStartHidden(t *testing.T) {
	old := os.Args
	defer func() { os.Args = old }()
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"app"}, false},
		{[]string{"app", "--start-hidden"}, true},
		{[]string{"app", "-x", "--start-hidden", "y"}, true},
		{[]string{"app", "--start-hidden=false"}, false},
		{[]string{"--start-hidden"}, false}, // the program name is not an argument
	} {
		os.Args = c.args
		if got := login.StartHidden(); got != c.want {
			t.Errorf("StartHidden() with %q = %v, want %v", c.args, got, c.want)
		}
	}
}

func TestAgentNames(t *testing.T) {
	for _, name := range []string{"", "a b", "../x", "-x", "a.b"} {
		if _, err := login.Agent(name).Status(); !errors.Is(err, login.ErrInvalid) {
			t.Errorf("Agent(%q).Status() = %v, want ErrInvalid", name, err)
		}
		if _, err := login.Agent(name).Enable(); !errors.Is(err, login.ErrInvalid) {
			t.Errorf("Agent(%q).Enable() = %v, want ErrInvalid", name, err)
		}
	}
}
