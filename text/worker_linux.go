package text

import "math"

type shapedFace struct {
	source           FaceID
	fileOff, fileLen uint32
	index, weight    int
	sizePx, scale    float32
	variable         bool
}

func (s *goSystem) exportFace(id FaceID, names *[]byte) shapedFace {
	f := &s.faces[id]
	r := shapedFace{source: id, fileOff: uint32(len(*names)), fileLen: uint32(len(f.key.file)),
		index: f.key.index, weight: f.weight, sizePx: f.sizePx, scale: f.owner, variable: f.key.weight != 0}
	*names = append(*names, f.key.file...)
	return r
}

func releaseShapedFace(*shapedFace) {}

func (s *goSystem) importFace(f *shapedFace, names []byte) (FaceID, bool) {
	// Each engine owns its font.Face, shaping plans and lazy bitmap readers.
	// Only the file identity crosses threads; no go-text caches are shared.
	path := names[f.fileOff : f.fileOff+f.fileLen]
	weight := 0
	if f.variable {
		weight = f.weight
	}
	for i := range s.faces {
		fc := &s.faces[i]
		k := fc.key
		if !fc.gone && k.index == f.index && k.weight == weight && k.size == math.Float32bits(f.sizePx) &&
			k.owner == math.Float32bits(f.scale) && k.file == string(path) {
			return FaceID(i), true
		}
	}
	ref := fontRef{file: string(path), index: f.index, variable: f.variable}
	id, err := s.newFace(ref, f.sizePx, f.weight, f.scale)
	return id, err == nil
}

func (s *goSystem) reserveWorkerScratch() {}
