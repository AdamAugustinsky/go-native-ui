//go:build darwin

package notify

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The center itself, in a real bundle: gnui run builds the demo
// (internal/demo) as a development app and launches it. Opt in:
//
//	GNUI_LIVE_NOTIFY=1 go test -run Live ./app/notify
//	    the check: Setup, the launch listing, Status and a post, without
//	    ever asking for permission (an app not allowed yet gets the system's
//	    refusal of the post; an allowed one posts and removes the note)
//	GNUI_LIVE_NOTIFY=hand go test -run Live -timeout 5m ./app/notify
//	    the round trip: asks for permission if needed (click Allow), posts a
//	    note with a reply action and waits for a reply typed into it
//
// The bundle is signed ad hoc unless GNUI_NOTIFY_DEVCERT=1 (the gnui-dev
// identity, whose grants survive rebuilds).
func TestLiveBundled(t *testing.T) {
	mode := os.Getenv("GNUI_LIVE_NOTIFY")
	if mode == "" {
		t.Skip("set GNUI_LIVE_NOTIFY=1 (or =hand) to run the demo in a bundle with gnui run")
	}
	hand := mode == "hand"
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	gnui := filepath.Join(dir, "gnui")
	build := exec.Command("go", "build", "-o", gnui, "./cmd/gnui")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building gnui: %v\n%s", err, out)
	}
	args := []string{"run", "-o", filepath.Join(dir, "dev"), "-c", "app/notify/internal/demo/gnui.json"}
	if os.Getenv("GNUI_NOTIFY_DEVCERT") == "" {
		args = append(args, "-adhoc")
	}
	limit := 90 * time.Second
	if hand {
		args = append(args, "--", "-mode", "hand", "-timeout", "4m")
		limit = 5 * time.Minute
		t.Log("a note from Notify Demo will appear: allow notifications if asked, then reply to the note")
	} else {
		args = append(args, "--", "-mode", "check", "-timeout", "40s")
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, gnui, args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("the demo did not end within %v\n%s", limit, out)
	}
	if err != nil {
		t.Fatalf("gnui run: %v\n%s", err, out)
	}
	kv := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line, ok := strings.CutPrefix(sc.Text(), "R ")
		if !ok {
			continue
		}
		for _, f := range strings.Fields(line) {
			if k, v, ok := strings.Cut(f, "="); ok {
				kv[k] = v
			}
		}
	}
	t.Logf("setup took %s µs; report %v", kv["setup_us"], kv)
	if kv["available"] != "true" || kv["setup_err"] != "none" || kv["categories_err"] != "none" || kv["restored"] == "" {
		t.Fatalf("the service did not start in the bundle\n%s", out)
	}
	status, err := strconv.Atoi(kv["status"])
	if err != nil || status < 0 || status > 4 {
		t.Fatalf("status %q\n%s", kv["status"], out)
	}
	if kv["timeout"] == "true" {
		t.Fatalf("the demo timed out\n%s", out)
	}
	if kv["blocks_live"] != "0" || kv["stale_calls"] != "0" || kv["slots_held"] != "0" {
		t.Fatalf("blocks %s live, %s stale calls, %s slots held at the end\n%s", kv["blocks_live"], kv["stale_calls"], kv["slots_held"], out)
	}
	if !hand {
		allowed := status == int(StatusAuthorized) || status == int(StatusProvisional)
		if kv["post_reservation"] != strconv.Itoa(int(Reserved)) || (allowed && kv["post_code"] != "0") || (!allowed && kv["post_code"] == "0") {
			t.Fatalf("post: reservation %s, code %s with status %d\n%s", kv["post_reservation"], kv["post_code"], status, out)
		}
		if !allowed {
			t.Logf("not allowed (status %d): the round trip needs GNUI_LIVE_NOTIFY=hand", status)
		}
		return
	}
	if kv["denied"] == "true" || kv["granted"] == "false" {
		t.Skipf("notifications are not allowed for Notify Demo (status %d)\n%s", status, out)
	}
	if kv["response_action"] != "1" || kv["response_reply"] == "" || kv["response_reply"] == `""` ||
		kv["response_userdata"] != `"demo-user-data"` || kv["response_ticket_matches"] != "true" {
		t.Fatalf("no reply came back\n%s", out)
	}
}
