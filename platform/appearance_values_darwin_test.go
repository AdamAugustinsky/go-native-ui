//go:build darwin

package platform

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/objc"
)

// nsColorIndependent reads system colour i as NSApp's appearance (not the
// current drawing appearance ReadSystemValues sets) resolves it, looked up in
// the "System" colour catalogue by name (not by its class method) and read
// with getRed:green:blue:alpha: (not the component getters): every step but
// NSColor itself differs from the reader's.
func nsColorIndependent(i SystemColor) (uint32, bool) {
	name := SystemColorNames[i]
	var c objc.ID
	if i == ColorAlternatingContentBackground {
		arr := objc.Send(apClsColor, objc.Sel(name))
		if arr == 0 {
			return 0, false
		}
		c = objc.Send1(arr, objc.Sel("objectAtIndex:"), 1)
	} else {
		c = objc.Send2(apClsColor, objc.Sel("colorWithCatalogName:colorName:"), uintptr(objc.Str("System")), uintptr(objc.Str(name)))
	}
	if c == 0 {
		return 0, false
	}
	c = objc.Send1(c, selUsingSpace, uintptr(objc.Send(apClsSpace, selSRGBSpace)))
	if c == 0 {
		return 0, false
	}
	var comp [4]float64
	get := objc.Sel("getRed:green:blue:alpha:") // before Reset: objc.Sel uses objc.F too
	objc.F.Reset().Arg(uintptr(c)).Arg(uintptr(get)).
		ArgPtr(unsafe.Pointer(&comp[0])).ArgPtr(unsafe.Pointer(&comp[1])).ArgPtr(unsafe.Pointer(&comp[2])).ArgPtr(unsafe.Pointer(&comp[3])).
		Call(objc.MsgSend)
	return rgba8(comp[0], comp[1], comp[2])&^0xFF | alpha8(comp[3]), true
}

// channelsWithin reports whether a and b differ by at most tol in every
// channel.
func channelsWithin(a, b uint32, tol int) bool {
	for sh := 0; sh < 32; sh += 8 {
		d := int(a>>sh&255) - int(b>>sh&255)
		if d < -tol || d > tol {
			return false
		}
	}
	return true
}

// The live values equal NSColor's, in both appearances, read another way.
func TestSystemValuesMatchNSColor(t *testing.T) {
	var v SystemValues
	var want [2][NumSystemColors]uint32
	var ok [2][NumSystemColors]bool
	onMain(func() {
		SystemAppearance() // the app follows the system
		ReadSystemValues(&v)
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		defer objc.Send1(apApp, selSetAppearance, 0)
		for d := range 2 {
			objc.Send1(apApp, selSetAppearance, uintptr(svNames[d]))
			for i := range NumSystemColors {
				want[d][i], ok[d][i] = nsColorIndependent(i)
			}
		}
	})
	if !v.Known {
		t.Fatal("system values not known")
	}
	n := 0
	for d, mode := range [2]string{"light", "dark"} {
		for i := range NumSystemColors {
			got := v.Colors[d][i]
			if !ok[d][i] {
				t.Logf("%s %s: no catalogue colour to compare (read %08X)", mode, SystemColorNames[i], got)
				continue
			}
			n++
			if !channelsWithin(got, want[d][i], 1) {
				t.Errorf("%s %s: read %08X, NSColor says %08X", mode, SystemColorNames[i], got, want[d][i])
			}
		}
	}
	t.Logf("%d colours compared; light label %08X, accent %08X, highlight %08X; dark window %08X; reduce motion %v, transparency %v, contrast %v, legacy scrollers %v",
		n, v.Colors[0][ColorLabel], v.Colors[0][ColorControlAccent], v.Colors[0][ColorSelectedTextBackground], v.Colors[1][ColorWindowBackground],
		v.ReduceMotion, v.ReduceTransparency, v.IncreaseContrast, v.LegacyScrollers)
	if n < int(NumSystemColors) {
		t.Errorf("only %d of %d colours compared", n, NumSystemColors)
	}
}

// The colours that do not depend on the user's settings are the committed
// reference (theme/testdata/appkit-ref.json) on the build it was measured on.
// On another macOS 27 build the differences are logged, not failed: Apple
// retunes a few between builds (27.2 26B5101f has a dark separatorColor of
// #FFFFFF1F where 26B5091g had #FFFFFF19).
func TestSystemValuesMatchReference(t *testing.T) {
	raw, err := os.ReadFile("../theme/testdata/appkit-ref.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		System struct {
			Build string `json:"osBuild"`
		} `json:"system"`
		Colors map[string]map[string]string `json:"colors"`
	}
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatal(err)
	}
	ver, _ := syscall.Sysctl("kern.osproductversion")
	build, _ := syscall.Sysctl("kern.osversion")
	if !strings.HasPrefix(ver, "27.") {
		t.Skipf("the reference is macOS 27's; this is %q", ver)
	}
	report := t.Errorf
	if !strings.Contains(ref.System.Build, "(Build "+build+")") {
		t.Logf("build %s, the reference's is %s: differences are logged", build, ref.System.Build)
		report = t.Logf
	}
	var v SystemValues
	onMain(func() {
		SystemAppearance()
		ReadSystemValues(&v)
	})
	if v.IncreaseContrast {
		t.Skip("Increase Contrast is on: the reference is the standard appearances'")
	}
	// The accent, the highlight and what follows them are the user's.
	user := map[SystemColor]bool{
		ColorControlAccent: true, ColorSelectedContentBackground: true, ColorKeyboardFocusIndicator: true,
		ColorSelectedTextBackground: true, ColorUnemphasizedSelectedTextBackground: true,
	}
	for d, mode := range [2]string{"light", "dark"} {
		for i := range NumSystemColors {
			if user[i] {
				continue
			}
			var want uint32
			if _, err := fmt.Sscanf(ref.Colors[mode][SystemColorNames[i]], "#%08X", &want); err != nil {
				t.Errorf("%s %s: no reference", mode, SystemColorNames[i])
				continue
			}
			if got := v.Colors[d][i]; !channelsWithin(got, want, 1) {
				report("%s %s: %08X, reference %08X", mode, SystemColorNames[i], got, want)
			}
		}
	}
}

// A change notification marks the values stale; the re-read allocates
// nothing, and a cached read is a copy.
func TestSystemValuesRefresh(t *testing.T) {
	var cached, reread float64
	var stale bool
	var before, after uint32
	var took time.Duration
	onMain(func() {
		SystemAppearance()
		var v SystemValues
		ReadSystemValues(&v)
		before = AppearanceSerial()
		postNotification("NSNotificationCenter", "NSPreferredScrollerStyleDidChangeNotification")
		after, stale = AppearanceSerial(), svStale.Load()
		t0 := time.Now()
		ReadSystemValues(&v)
		took = time.Since(t0)
		cached = testing.AllocsPerRun(100, func() { ReadSystemValues(&v) })
		reread = testing.AllocsPerRun(20, func() {
			svStale.Store(true)
			ReadSystemValues(&v)
		})
	})
	if after == before || !stale {
		t.Errorf("scroller-style notification: serial %d -> %d, stale %v", before, after, stale)
	}
	if cached != 0 || reread != 0 {
		t.Errorf("allocations: cached read %v, re-read %v", cached, reread)
	}
	t.Logf("a full read (%d colours in two appearances and the settings): %v", NumSystemColors, took)
	if took > 5*time.Millisecond {
		t.Errorf("a read took %v", took)
	}
}

// Before the app follows the system (no SystemAppearance call yet), nothing
// is read: an offscreen render that pins a mode never sees this machine.
func TestSystemValuesNeedFollowing(t *testing.T) {
	was := apFollowing.Swap(false) // as before any SystemAppearance call
	defer apFollowing.Store(was)
	var v SystemValues
	v.Known, v.ReduceMotion = true, true
	ReadSystemValues(&v)
	if v != (SystemValues{}) {
		t.Error("values read before the app follows the system")
	}
}
