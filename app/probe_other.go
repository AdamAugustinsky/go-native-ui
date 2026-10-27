//go:build !darwin

package app

func probeBegin()                        {}
func probeFrame(m *frameMeasure)         {}
func probeEnd(n int, mainUs float64, a *App) {}

var probeW, probeH int
