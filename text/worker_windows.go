package text

import (
	"math"

	"gonativeui/internal/com"
)

type shapedFace struct {
	source           FaceID
	face             uintptr // retained IDWriteFontFace; shared factory
	sizePx, scale    float32
	color            bool
	keyOff, keyLen   uint32
	nameOff, nameLen uint32
}

func (s *System) exportFace(id FaceID, names *[]byte) shapedFace {
	f := &s.faces[id]
	r := shapedFace{source: id, face: f.face, sizePx: f.sizePx, scale: f.scale, color: f.color,
		keyOff: uint32(len(*names)), keyLen: uint32(len(f.key))}
	*names = append(*names, f.key...)
	r.nameOff, r.nameLen = uint32(len(*names)), uint32(len(f.name))
	*names = append(*names, f.name...)
	com.Call(f.face, 1) // IUnknown::AddRef
	return r
}

func releaseShapedFace(f *shapedFace) { com.Release(f.face) }

func (s *System) importFace(f *shapedFace, names []byte) (FaceID, bool) {
	key := names[f.keyOff : f.keyOff+f.keyLen]
	if id, ok := s.faceByKey[string(key)]; ok {
		return id, true
	}
	free := -1
	for i := range s.faces {
		if s.faces[i].gone {
			free = i
			break
		}
	}
	if free < 0 && len(s.faces) >= math.MaxUint16 {
		return 0, false
	}
	fc := faceEnt{face: f.face, sizePx: f.sizePx, scale: f.scale, color: f.color,
		key: string(key), name: string(names[f.nameOff : f.nameOff+f.nameLen])}
	com.Call(fc.face, 1)
	if free < 0 {
		free = len(s.faces)
		s.faces = append(s.faces, fc)
	} else {
		s.faces[free] = fc
	}
	id := FaceID(free)
	s.faceByKey[fc.key] = id
	return id, true
}

func (s *System) reserveWorkerScratch() {}
