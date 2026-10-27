// Throwaway no-window startup probe.
package main

import (
	"gonativeui/internal/sysinfo"
)

func main() { println(sysinfo.ProcessAgeNs()) }
