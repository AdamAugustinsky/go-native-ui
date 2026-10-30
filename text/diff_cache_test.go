package text

import (
	"bytes"
	"slices"
	"sort"
	"testing"
)

type diffRunKey struct {
	font FontKey
	text string
}
type diffRunEntry struct {
	last uint64
	view RunView
}
type diffRunModel struct {
	entries            map[diffRunKey]diffRunEntry
	over               *diffRunKey
	overView           RunView
	clock              uint64
	gen                uint32
	age                int
	runs, glyphs, text int
}

func diffCopyRun(v RunView) RunView {
	v.Text = slices.Clone(v.Text)
	v.Glyphs = slices.Clone(v.Glyphs)
	v.X = slices.Clone(v.X)
	v.Faces = slices.Clone(v.Faces)
	v.Clusters = slices.Clone(v.Clusters)
	return v
}

func diffEqualRun(t *testing.T, got, want RunView) {
	t.Helper()
	if !bytes.Equal(got.Text, want.Text) || !slices.Equal(got.Glyphs, want.Glyphs) || !slices.Equal(got.X, want.X) || !slices.Equal(got.Clusters, want.Clusters) || got.Advance != want.Advance {
		t.Fatalf("cached shape differs from fresh shape: got=%+v want=%+v", got, want)
	}
}

func (m *diffRunModel) lookup(k diffRunKey) bool {
	if m.over != nil && *m.over == k {
		m.age = 0
		return true
	}
	if e, ok := m.entries[k]; ok {
		m.clock++
		e.last = m.clock
		m.entries[k] = e
		return true
	}
	return false
}

func (m *diffRunModel) usage() (text, glyphs int) {
	for _, e := range m.entries {
		text += len(e.view.Text)
		glyphs += len(e.view.Glyphs)
	}
	return
}

// Sort by recency, grouping whole epochs. This map model neither compacts
// slabs nor probes a hash table; it implements the specified bucket policy.
func (m *diffRunModel) evict(text, glyphs int) {
	m.gen++
	oldest := m.clock
	keys := make([]diffRunKey, 0, len(m.entries))
	for k, e := range m.entries {
		keys = append(keys, k)
		oldest = min(oldest, e.last)
	}
	step := (m.clock-oldest)/256 + 1
	sort.Slice(keys, func(i, j int) bool { return m.entries[keys[i]].last > m.entries[keys[j]].last })
	keep := make(map[diffRunKey]diffRunEntry)
	usedT, usedG := 0, 0
	for i := 0; i < len(keys); {
		j := i
		nt, ng := 0, 0
		bucket := (m.entries[keys[i]].last - oldest) / step
		for j < len(keys) && (m.entries[keys[j]].last-oldest)/step == bucket {
			e := m.entries[keys[j]]
			nt += len(e.view.Text)
			ng += len(e.view.Glyphs)
			j++
		}
		if len(keep)+j-i > min(m.runs-1, m.runs*3/4) || usedT+nt > min(m.text-text, m.text*3/4) || usedG+ng > min(m.glyphs-glyphs, m.glyphs*3/4) {
			break
		}
		for _, k := range keys[i:j] {
			keep[k] = m.entries[k]
		}
		usedT += nt
		usedG += ng
		i = j
	}
	m.entries = keep
}

func (m *diffRunModel) insert(k diffRunKey, v RunView) {
	if len(v.Text) > m.text || len(v.Glyphs) > m.glyphs || m.runs < 1 {
		m.gen++
		m.over = &k
		m.overView = diffCopyRun(v)
		m.age = 0
		return
	}
	if m.over != nil {
		m.age++
		if m.age > m.runs {
			m.over = nil
			m.age = 0
			m.gen++
		}
	}
	nt, ng := m.usage()
	if len(m.entries) >= m.runs || nt+len(v.Text) > m.text || ng+len(v.Glyphs) > m.glyphs {
		m.evict(len(v.Text), len(v.Glyphs))
	}
	m.clock++
	m.entries[k] = diffRunEntry{m.clock, diffCopyRun(v)}
}

// Up to 768 commands, 16 names of <=64 bytes, and cache limits <=16 runs,
// 64 glyphs/128 text bytes plus one <=64-byte oversize run. O(commands*16²)
// model work; all storage is bounded independently of the command count.
func FuzzRunCacheDifferential(f *testing.F) {
	for _, s := range diffTextSeeds {
		f.Add([]byte(s), []byte{4, 16, 32, 0, 1, 2, 0, 2, 3, 0, 1, 2, 1, 1, 2, 2, 0, 0, 3, 0, 0})
	}
	// Leave a cold epoch behind while a hot run spans several epoch buckets.
	trace := []byte{4, 64, 128, 0, 0, 0, 0, 1, 0}
	for range 520 {
		trace = append(trace, 1, 1, 0)
	}
	trace = append(trace, 0, 2, 0, 0, 3, 0, 0, 4, 0, 2, 0, 0)
	f.Add([]byte("cold\x00hot\x00office fi\x00日本語\x00new"), trace)
	s := workerSystem(f)
	fonts := []FontKey{s.Font(FontDesc{SizePt: 13, Scale: 1}), s.Font(FontDesc{SizePt: 16, Scale: 1})}
	f.Fuzz(func(t *testing.T, data, ops []byte) {
		if len(data) > 256 || len(ops) > 2307 {
			return
		}
		defer diffDeadline(t)()
		names := bytes.Split(data, []byte{0})
		names = names[:min(len(names), 16)]
		for _, b := range names {
			if len(b) > 64 {
				return
			}
		}
		if len(ops) < 3 {
			return
		}
		c := newRunCache()
		c.maxRuns = int(ops[0] % 17)
		c.maxGlyphs = int(ops[1] % 65)
		c.maxText = int(ops[2] % 129)
		m := diffRunModel{entries: make(map[diffRunKey]diffRunEntry), runs: c.maxRuns, glyphs: c.maxGlyphs, text: c.maxText}
		for i := 3; i+2 < len(ops); i += 3 {
			b := names[int(ops[i+1])%len(names)]
			font := fonts[ops[i+2]%2]
			k := diffRunKey{font, string(b)}
			switch ops[i] % 5 {
			case 0, 1:
				ref, hit := c.lookup(font, b)
				want := m.lookup(k)
				if hit != want {
					t.Fatalf("step %d: hit=%v model=%v key=%+v", i/3, hit, want, k)
				}
				if hit || ops[i]%5 == 0 {
					s.runs.drop()
					fresh := s.Run(s.Shape(font, b))
					if !hit {
						ref = c.insert(font, b, fresh.Glyphs, fresh.X, fresh.Faces, fresh.Clusters, fresh.Advance)
						m.insert(k, fresh)
					}
					diffEqualRun(t, c.view(ref), fresh)
				}
			case 2:
				c.evict()
				m.evict(0, 0)
			case 3:
				c.drop()
				clear(m.entries)
				m.over = nil
				m.age = 0
				m.gen++
			case 4:
				c.reserve(int(ops[i+1]), int(ops[i+2]), int(ops[i+1]))
			}
			if c.gen != m.gen || c.clock != m.clock || len(c.ents) != len(m.entries) || c.overOK != (m.over != nil) || c.overAge != m.age {
				t.Fatalf("step=%d state gen=%d/%d clock=%d/%d entries=%d/%d over=%v/%v age=%d/%d", i/3, c.gen, m.gen, c.clock, m.clock, len(c.ents), len(m.entries), c.overOK, m.over != nil, c.overAge, m.age)
			}
			if len(c.ents) > c.maxRuns || cap(c.ents) > c.maxRuns || cap(c.text) > c.maxText || cap(c.glyph) > c.maxGlyphs || cap(c.xs) > c.maxGlyphs || cap(c.faces) > c.maxGlyphs || cap(c.clus) > c.maxGlyphs {
				t.Fatal("cache exceeded hard budget")
			}
			for j, e := range c.ents {
				v := c.view(RunRef(j + 1))
				want, ok := m.entries[diffRunKey{e.font, string(v.Text)}]
				if !ok || e.last != want.last {
					t.Fatal("wrong resident epoch")
				}
				diffEqualRun(t, v, want.view)
				if !slices.Equal(v.Faces, want.view.Faces) {
					t.Fatal("compaction changed faces")
				}
			}
			if m.over != nil {
				diffEqualRun(t, c.view(overRunRef), m.overView)
			}
		}
	})
}
