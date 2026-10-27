//go:build darwin && perfmem

package gpu

import "gonativeui/internal/objc"

// PerfMemory is throwaway accounting of requested renderer resources, not RSS.
func (r *Renderer) PerfMemory() map[string]uint64 {
	m := map[string]uint64{}
	allocated := objc.Sel("allocatedSize")
	for _, s := range r.slots {
		m["InstanceBufferBytes"] += uint64(s.quads * quadSize)
		m["StageBufferBytes"] += uint64(s.stageCap)
	}
	for _, t := range r.textures {
		if t != 0 {
			m["TextureAllocatedBytes"] += uint64(objc.Send(t, allocated))
			m["TextureCount"]++
		}
	}
	return m
}
