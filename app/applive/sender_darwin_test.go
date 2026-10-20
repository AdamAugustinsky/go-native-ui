//go:build darwin

package applive

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"gonativeui/internal/objc"
)

// The quit Apple event as a real sender sends it: from another process (the
// Dock, loginwindow), built the SDK's way (kAEQuitApplication, with the
// kAEQuitReason parameter for a logout, restart or shutdown), sent with
// AESendMessage ([NSAppleEventDescriptor sendEventWithOptions:...]) and
// kAEWaitReply, so that the reply (a suspended event's, when it comes) and
// its error number are what the sender receives.

// startQuitSender starts the sender: a child of the scenario's process (the
// test binary again) that sends the quit event to pid with reason (a
// four-character code such as "rlgo"; "" for none) and appends
// "R errn=… send_err=… took_ms=…" to out once the reply came.
func startQuitSender(pid int, reason, out string) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "GNUI_APPLIVE=aesend", "TARGET="+strconv.Itoa(pid), "REASON="+reason, "OUT="+out)
	if err := cmd.Start(); err != nil {
		fail("sender: %v", err)
	}
	go cmd.Wait()
}

// fourCC is s as an OSType.
func fourCC(s string) uintptr {
	if len(s) != 4 {
		return 0
	}
	return uintptr(s[0])<<24 | uintptr(s[1])<<16 | uintptr(s[2])<<8 | uintptr(s[3])
}

// readSender waits up to d for the sender's line in out and returns its keys.
func readSender(out string, d time.Duration) map[string]string {
	kv := map[string]string{}
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		b, _ := os.ReadFile(out)
		if s := string(b); strings.HasPrefix(s, "R ") {
			for _, f := range strings.Fields(s[2:]) {
				if k, v, ok := strings.Cut(f, "="); ok {
					kv[k] = v
				}
			}
			return kv
		}
	}
	return kv
}

func init() {
	// aesend: the sender's process (startQuitSender).
	scenarios["aesend"] = func() {
		pid, _ := strconv.Atoi(os.Getenv("TARGET"))
		cls := objc.Cls("NSAppleEventDescriptor")
		target := objc.Send1(cls, objc.Sel("descriptorWithProcessIdentifier:"), uintptr(pid))
		selMake := objc.Sel("appleEventWithEventClass:eventID:targetDescriptor:returnID:transactionID:")
		ev := objc.ID(objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(selMake)).Arg(fourCC("aevt")).Arg(fourCC("quit")).
			Arg(uintptr(target)).Arg(^uintptr(0) /* kAutoGenerateReturnID */).Arg(0 /* kAnyTransactionID */).Call(objc.MsgSend).R0())
		if r := os.Getenv("REASON"); r != "" {
			why := objc.Send1(cls, objc.Sel("descriptorWithEnumCode:"), fourCC(r))
			objc.Send2(ev, objc.Sel("setParamDescriptor:forKeyword:"), uintptr(why), fourCC("why?")) // kAEQuitReason, a parameter
		}
		selSend := objc.Sel("sendEventWithOptions:timeout:error:")
		errOut := new(uintptr)
		t0 := time.Now()
		reply := objc.ID(objc.F.Reset().Arg(uintptr(ev)).Arg(uintptr(selSend)).Arg(3 /* kAEWaitReply */).ArgF64(60).
			ArgPtr(unsafe.Pointer(errOut)).Call(objc.MsgSend).R0())
		errn, sendErr := 0, 0
		if *errOut != 0 {
			sendErr = int(int64(objc.Send(objc.ID(*errOut), objc.Sel("code"))))
		} else if d := objc.Send1(reply, objc.Sel("paramDescriptorForKeyword:"), fourCC("errn")); d != 0 {
			errn = int(int32(uint32(objc.Send(d, objc.Sel("int32Value")))))
		}
		line := fmt.Sprintf("R errn=%d send_err=%d took_ms=%d\n", errn, sendErr, time.Since(t0).Milliseconds())
		if err := os.WriteFile(os.Getenv("OUT"), []byte(line), 0o644); err != nil {
			fail("sender: %v", err)
		}
	}
}
