package text

import (
	"strconv"
	"testing"
)

// One-use scrolling content must not displace the repeatedly used toolbar.
func TestCacheChurnKeepsReusedEntries(t *testing.T) {
	c := newRunCache()
	c.maxRuns = 256
	c.reserve(256, 0, 1<<16)
	pc := newParaCache()
	pc.reserve(ParaBudget{Paras: 256, Glyphs: 4096, Text: 65536}, true)
	var hotMiss, paraHotMiss int
	var buf [32]byte
	for i := 0; i < 10000; i++ {
		for j := 0; j < 10; j++ {
			key := strconv.AppendInt(append(buf[:0], "hot"...), int64(j), 10)
			if _, ok := c.lookup(0, key); !ok {
				hotMiss++
				c.insert(0, key, nil, nil, nil, nil, 0)
			}
			p := Para{Text: key}
			h := hashPara(&p)
			if _, ok := pc.lookup(&p, h); !ok {
				paraHotMiss++
				pc.insert(&p, h, &paraBuild{})
			}
		}
		key := strconv.AppendInt(append(buf[:0], "cold"...), int64(i), 10)
		c.insert(0, key, nil, nil, nil, nil, 0)
		p := Para{Text: key}
		pc.insert(&p, hashPara(&p), &paraBuild{})
	}
	t.Logf("hot misses: run=%d paragraph=%d; evictions: run=%d paragraph=%d", hotMiss, paraHotMiss, c.gen, pc.gen)
	if hotMiss != 10 || paraHotMiss != 10 {
		t.Fatal("reused entries were evicted by one-use entries")
	}
	if c.gen != 40 || pc.gen != 40 {
		t.Fatal("expected 40 evictions in each cache")
	}
}
