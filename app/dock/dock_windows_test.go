//go:build windows

package dock

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/internal/win32"
	"gonativeui/platform"
	"gonativeui/ui"
)

// The taskbar (S1) on the CI runners: the badge's icon read back from the
// system, and the app's progress and badge on real windows' taskbar
// buttons, in a child process running app.Main on its main thread. The
// shell gives no read-back of a button's progress or overlay: the calls'
// results, the hook for a button made later, and the GDI and USER objects
// are checked; how they look is the hand checklist's.

func init() { runtime.LockOSThread() }

func TestMain(m *testing.M) {
	if os.Getenv("GNUI_DOCK_CHILD") != "" {
		taskbarChild()
		return
	}
	os.Exit(m.Run())
}

var (
	getGuiResources = syscall.NewLazyDLL("user32.dll").NewProc("GetGuiResources")
	getCurrentProc  = syscall.NewLazyDLL("kernel32.dll").NewProc("GetCurrentProcess")
)

func guiObjects() (gdi, user int) {
	p, _, _ := getCurrentProc.Call()
	g, _, _ := getGuiResources.Call(p, 0)
	u, _, _ := getGuiResources.Call(p, 1)
	return int(g), int(u)
}

// checkBadge reads icon back and checks its disc and text.
func checkBadge(icon uintptr, n int) error {
	px, w, h := win32.IconPixels(icon)
	if w != n || h != n {
		return fmt.Errorf("the icon is %d×%d, want %d×%d", w, h, n, n)
	}
	at := func(x, y int) (a, r, g, b uint32) {
		p := px[y*n+x]
		return p >> 24, p >> 16 & 0xFF, p >> 8 & 0xFF, p & 0xFF
	}
	for _, c := range [][2]int{{0, 0}, {n - 1, 0}, {0, n - 1}, {n - 1, n - 1}} {
		if a, _, _, _ := at(c[0], c[1]); a != 0 {
			return fmt.Errorf("corner %v is not transparent (alpha %d)", c, a)
		}
	}
	if a, r, g, b := at(n/8+1, n/2); a < 200 || r < 180 || g > 120 {
		return fmt.Errorf("the disc's left edge is %d,%d,%d alpha %d, want badge red", r, g, b, a)
	}
	white := 0
	for y := n / 4; y < 3*n/4; y++ {
		for x := n / 4; x < 3*n/4; x++ {
			if a, r, g, b := at(x, y); a > 200 && r > 220 && g > 200 && b > 200 {
				white++
			}
		}
	}
	if white < n/4 {
		return fmt.Errorf("%d white pixels in the middle: no text", white)
	}
	return nil
}

func TestBadgeIcon(t *testing.T) {
	gdi0, user0 := guiObjects()
	for _, n := range []int{16, 20, 24, 32} {
		for _, label := range []string{"7", "42", "999+", "!"} {
			icon, err := drawBadge(label, n)
			if err != nil {
				t.Fatal(err)
			}
			if err := checkBadge(icon, n); err != nil {
				t.Errorf("%q at %d px: %v", label, n, err)
			}
			win32.DestroyIcon(icon)
		}
	}
	for i := 0; i < 50; i++ {
		icon, _ := drawBadge(strconv.Itoa(i), 16)
		win32.DestroyIcon(icon)
	}
	if gdi, user := guiObjects(); gdi > gdi0+2 || user > user0+2 {
		t.Errorf("GDI %d → %d, USER %d → %d after 66 badges", gdi0, gdi, user0, user)
	}
}

func TestTaskbarLive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "GNUI_DOCK_CHILD=1")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	kv := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(out.String()))
	for sc.Scan() {
		if line, ok := strings.CutPrefix(sc.Text(), "R "); ok {
			for _, f := range strings.Fields(line) {
				if k, v, ok := strings.Cut(f, "="); ok {
					kv[k] = v
				}
			}
		}
	}
	if err != nil || ctx.Err() != nil {
		t.Fatalf("child: %v\n%s", err, out.String())
	}
	t.Logf("%s", out.String())
	for k, want := range map[string]string{
		"progress_err": "nil", "progress_hr": "0", "indeterminate_err": "nil", "badge_err": "nil", "badge": "3",
		"badge_icon": "ok", "progress": "0.25", "cleared_icon": "0", "notstarted": "true",
	} {
		if kv[k] != want {
			t.Errorf("%s = %q, want %q", k, kv[k], want)
		}
	}
	if n, _ := strconv.Atoi(kv["applied_first"]); n < 1 {
		t.Errorf("the state was applied to %q windows, want the open one", kv["applied_first"])
	}
	if n, _ := strconv.Atoi(kv["buttons"]); n < 1 {
		t.Errorf("%q taskbar buttons made after the hook, want the second window's", kv["buttons"])
	}
	if n, _ := strconv.Atoi(kv["gdi_growth"]); n > 2 {
		t.Errorf("GDI objects grew by %s over 40 badge changes", kv["gdi_growth"])
	}
	if n, _ := strconv.Atoi(kv["user_growth"]); n > 2 {
		t.Errorf("USER objects grew by %s over 40 badge changes", kv["user_growth"])
	}
}

// ---- The child ----

func say(format string, args ...any) { fmt.Printf("R "+format+"\n", args...) }

type blank struct{}

func (blank) Build(*ui.Context) {}

type steps struct {
	app.DefaultHandler
	start func(a *app.App) error
	list  []func(a *app.App)
	next  int
}

func (h *steps) Start(a *app.App) error { return h.start(a) }

func (h *steps) Event(a *app.App, e *app.Event) {
	if e.Kind == app.EvPost && e.Source == platform.SourceApp && h.next < len(h.list) {
		h.next++
		h.list[h.next-1](a)
		return
	}
	a.DefaultEvent(e)
}

// after posts the next step d from now, from one thread (a thread that
// posts becomes a GUI thread and keeps a USER object).
var posts = make(chan time.Duration, 8)

func after(d time.Duration) { posts <- d }

func poster() {
	runtime.LockOSThread()
	for d := range posts {
		time.Sleep(d)
		for !app.Post(app.Event{Source: platform.SourceApp}, nil) {
			time.Sleep(time.Millisecond)
		}
	}
}

func errText(err error) string {
	if err == nil {
		return "nil"
	}
	return strings.ReplaceAll(err.Error(), " ", "_")
}

func taskbarChild() {
	time.AfterFunc(45*time.Second, func() { fmt.Println("FAIL watchdog"); os.Exit(3) })
	errBadge := SetBadge("x")
	say("notstarted=%v", errors.Is(SetProgress(0.5), ErrNotStarted) && errors.Is(errBadge, ErrNotStarted))
	go poster()
	cfg := app.WindowConfig{Title: "dock test", Width: 320, Height: 200}
	var gdi0, user0 int
	h := &steps{}
	h.start = func(a *app.App) error {
		_, err := a.OpenWindow(cfg, blank{})
		after(800 * time.Millisecond)
		return err
	}
	h.list = []func(*app.App){
		func(a *app.App) {
			err := SetProgress(0.5)
			say("progress_err=%s progress_hr=%d applied_first=%d", errText(err), tb.lastHR, tb.applied)
			say("indeterminate_err=%s", errText(SetProgress(Indeterminate)))
			err = SetBadgeCount(3)
			say("badge_err=%s badge=%s", errText(err), Badge())
			if e := checkBadge(tb.icon, smallIconSize()); e != nil {
				say("badge_icon=%s", errText(e))
			} else {
				say("badge_icon=ok")
			}
			gdi0, user0 = guiObjects()
			for i := 0; i < 40; i++ {
				SetBadgeCount(i % 7)
			}
			SetBadgeCount(3)
			gdi, user := guiObjects()
			say("gdi_growth=%d user_growth=%d", gdi-gdi0, user-user0)
			SetProgress(0.25)
			if _, err := a.OpenWindow(cfg, blank{}); err != nil {
				say("second_window=%s", errText(err))
			}
			after(800 * time.Millisecond)
		},
		func(a *app.App) {
			say("buttons=%d progress=%v", tb.buttons, Progress())
			id, err := Bounce(true)
			say("bounce=%d bounce_err=%s", id, errText(err))
			CancelBounce(id)
			SetBadgeCount(0)
			say("cleared_icon=%d", tb.icon)
			SetProgress(NoProgress)
			a.Quit()
		},
	}
	if err := app.Main(app.AppConfig{ID: "dev.gonativeui.docktest"}, h); err != nil {
		fmt.Println("FAIL main:", err)
		os.Exit(2)
	}
}
