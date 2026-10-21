//go:build !linux

package fs

// prewarmScratch has no scratch to make ready: only Linux keeps working
// memory for its listings that the first overlapping ones would allocate.
func prewarmScratch(n int) {}
