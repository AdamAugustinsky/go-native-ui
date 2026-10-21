package text

import (
	"bytes"
	"strconv"
	"testing"
)

// Even a full hash collision must compare font, length and every text byte.
func TestRunCacheHashCollisions(t *testing.T) {
	for _, n := range []int{0, 1, 4, 8, 15, 16, 17, 128, 1024, 16384} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			key := bytes.Repeat([]byte{0xff}, n) // arbitrary bytes, not only UTF-8
			for variant := -2; variant < n; variant++ {
				other, font := bytes.Clone(key), FontKey(1)
				switch variant {
				case -2:
					font++
				case -1:
					other = append(other, 0)
				default:
					other[variant] ^= 1
				}
				c := newRunCache()
				c.insert(font, other, nil, nil, nil, nil, 1)
				c.ents[0].hash = hashRun(1, key)
				c.rehash(len(c.table))
				if _, ok := c.lookup(1, key); ok {
					t.Fatalf("false hit: differing font/length/byte %d", variant)
				}
				c.insert(1, key, nil, nil, nil, nil, 2)
				r, ok := c.lookup(1, key)
				if !ok || c.view(r).Advance != 2 {
					t.Fatalf("collision hid the matching key: variant %d", variant)
				}
				c.evict()
				r, ok = c.lookup(1, key)
				if c.gen != 1 || !ok || c.view(r).Advance != 2 {
					t.Fatalf("eviction lost the matching key: variant %d", variant)
				}
			}
		})
	}
}

func BenchmarkRunCacheHitBytes(b *testing.B) {
	for _, n := range []int{0, 1, 4, 8, 16, 128, 1024, 16384} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			c := newRunCache()
			key := bytes.Repeat([]byte("a"), n)
			c.insert(1, key, nil, nil, nil, nil, 0)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, ok := c.lookup(1, key); !ok {
					b.Fatal("cache miss")
				}
			}
		})
	}
}
