package main

// cpuTime is not measured on Windows (0).
func cpuTime() int64 { return 0 }
