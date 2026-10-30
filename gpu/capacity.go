package gpu

// Instance buffers start in the first demand's size class. A slot shrinks
// only after 60 uses at no more than a quarter of its capacity.
const initialQuads = 64
const instanceCalmFrames = 60
const runTextures = 8

type instanceDemand struct {
	peak, calm int
}

func sizeClass(n, floor int) int {
	for floor < n {
		floor *= 2
	}
	return floor
}

// capacity is the next capacity, with headroom after sustained low demand.
// The caller replaces storage only after retiring or orphaning its old use.
func (d *instanceDemand) capacity(current, need int) int {
	if need > current {
		*d = instanceDemand{}
		return sizeClass(need, initialQuads)
	}
	if current <= initialQuads || need > current/4 {
		*d = instanceDemand{}
		return current
	}
	d.peak = max(d.peak, need)
	d.calm++
	if d.calm < instanceCalmFrames {
		return current
	}
	n := sizeClass(2*d.peak, initialQuads)
	*d = instanceDemand{}
	return n
}
