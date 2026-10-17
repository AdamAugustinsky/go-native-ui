//go:build darwin

package platform

import (
	"runtime"
	"strings"
	"testing"
)

// The clipboard round trip through the real general pasteboard. It restores
// what was on the clipboard, and skips where the pasteboard cannot be
// reached (a sandboxed or locked session, a headless runner).
func TestClipboardRoundTrip(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	orig := Clipboard()
	defer SetClipboard(orig)

	probe := "gonativeui clipboard probe \u00e9"
	SetClipboard(probe)
	if got := Clipboard(); got != probe {
		if got == orig {
			t.Skipf("the pasteboard cannot be written here (sandboxed or locked session?); still holds %q", orig)
		}
		t.Fatalf("round trip: got %q, want %q", got, probe)
	}
	for _, s := range []string{
		"h\u00e9llo w\u00f6rld\n\u65e5\u672c\u8a9e \U0001F468\u200D\U0001F469\u200D\U0001F467\tend",
		"",
		strings.Repeat("0123456789abcdef", 1<<16), // 1 MiB
		"a\xffb", // invalid UTF-8 becomes U+FFFD, the text survives
	} {
		SetClipboard(s)
		want := strings.ToValidUTF8(s, "\uFFFD")
		if got := Clipboard(); got != want && !(want == "" && got != "") {
			t.Errorf("round trip of %d bytes: got %d bytes", len(want), len(got))
		}
	}
}
