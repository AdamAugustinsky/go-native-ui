package appkitref

import "syscall"

// host is this machine's hw.model and macOS build.
func host() (model, build string) {
	model, _ = syscall.Sysctl("hw.model")
	build, _ = syscall.Sysctl("kern.osversion")
	return
}
