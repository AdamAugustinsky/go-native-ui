package app

import "slices"

// Stats summarises a sample set.
type Stats struct {
	// P50, P99, Max and Mean are the median, the 99th percentile, the maximum
	// and the arithmetic mean, in the unit of the samples.
	P50, P99, Max, Mean float64
}

// Summarize returns the percentiles, maximum and mean of xs (xs is not
// modified). It allocates; call it at report time.
func Summarize(xs []float64) Stats {
	if len(xs) == 0 {
		return Stats{}
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	var sum float64
	for _, x := range s {
		sum += x
	}
	return Stats{P50: s[len(s)/2], P99: s[len(s)*99/100], Max: s[len(s)-1], Mean: sum / float64(len(s))}
}

// sampler is a fixed-capacity sample ring: once full the oldest are
// overwritten. add does not allocate.
type sampler struct {
	buf []float64
	n   int
}

func newSampler(capacity int) sampler { return sampler{buf: make([]float64, capacity)} }

func (s *sampler) add(v float64) {
	if len(s.buf) == 0 {
		return
	}
	s.buf[s.n%len(s.buf)] = v
	s.n++
}

// values returns the retained samples, in no particular order once wrapped.
func (s *sampler) values() []float64 { return s.buf[:min(s.n, len(s.buf))] }
