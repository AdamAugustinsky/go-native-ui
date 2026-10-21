//go:build !linux && !darwin

package fs

// prewarmScratch has no scratch to make ready: only Linux and macOS keep
// working memory for their listings that the first overlapping ones would
// allocate.
func prewarmScratch(n int) {}
