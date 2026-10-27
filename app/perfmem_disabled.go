//go:build !perfmem

package app

func perfAfterIdle(*App) {}

func perfIdle(*App)               {}
func perfCheckpoint(*App, string) {}
func perfCPU() func()             { return func() {} }
