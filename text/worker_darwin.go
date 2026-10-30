package text

import (
	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
)

type shapedFace struct {
	source         FaceID
	font           uintptr // retained immutable CTFont, including its variation axes
	scale          float32
	keyOff, keyLen uint32
}

func (s *System) exportFace(id FaceID, names *[]byte) shapedFace {
	f := &s.faces[id]
	s.retain(f.font)
	r := shapedFace{source: id, font: f.font, scale: f.scale, keyOff: uint32(len(*names)), keyLen: uint32(len(f.key))}
	*names = append(*names, f.key...)
	return r
}

func releaseShapedFace(f *shapedFace) {
	var fr ffi.Frame
	cf.CFRelease(&fr, f.font)
}

func (s *System) importFace(f *shapedFace, names []byte) (FaceID, bool) {
	// Use the same canonical identity as faceFor. CoreText can attach
	// different cascade attributes to equal raster faces (CFEqual is false).
	key := names[f.keyOff : f.keyOff+f.keyLen]
	if id, ok := s.faceByName[string(key)]; ok {
		return id, true
	}
	id := s.faceFor(f.font, f.scale)
	return id, int(id) < len(s.faces) && !s.faces[id].gone && s.faces[id].key == string(key)
}

func (s *System) reserveWorkerScratch() { s.reserveScratch(1024) }
