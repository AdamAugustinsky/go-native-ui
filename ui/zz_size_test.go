package ui

import (
	"reflect"
	"testing"
	"unsafe"
	"gonativeui/gpu"
)

func TestZZSizes(t *testing.T) {
	var b box
	t.Logf("box=%d persist=%d wrapCache=%d flowOut=%d richPrev=%d flowSpec=%d Quad=%d Context=%d", unsafe.Sizeof(b), unsafe.Sizeof(b.persist), unsafe.Sizeof(b.wrap), unsafe.Sizeof(b.flowOut), unsafe.Sizeof(b.rp), unsafe.Sizeof(b.flow), unsafe.Sizeof(gpu.Quad{}), unsafe.Sizeof(Context{}))
	t.Logf("off flags=%d bg=%d pref=%d txt=%d sem=%d flow=%d", unsafe.Offsetof(b.flags), unsafe.Offsetof(b.bg), unsafe.Offsetof(b.pref), unsafe.Offsetof(b.txt), unsafe.Offsetof(b.sem), unsafe.Offsetof(b.flow))
}

func TestZZSceneCounts(t *testing.T) {
	s := newScene(newFake(), 1280, 800)
	s.script = false
	s.scroll = -26
	s.c.AtlasSync(&fakeUploader{})
	s.in.MouseX, s.in.MouseY = 100, 300
	step := func() { s.in.AddScroll(0, s.scroll, true); s.step() }
	for i := 0; i < 700; i++ {
		step()
	}
	PerfReset()
	for i := 0; i < 1000; i++ {
		step()
	}
	t.Log(PerfReport(1000))
}

func TestZZPointerFree(t *testing.T) {
	var hasPtr func(reflect.Type) bool
	hasPtr = func(ty reflect.Type) bool {
		switch ty.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map, reflect.String, reflect.Interface, reflect.Func, reflect.Chan, reflect.UnsafePointer:
			return true
		case reflect.Array:
			return hasPtr(ty.Elem())
		case reflect.Struct:
			for i := 0; i < ty.NumField(); i++ {
				if hasPtr(ty.Field(i).Type) {
					t.Logf("pointer field %s.%s", ty.Name(), ty.Field(i).Name)
					return true
				}
			}
		}
		return false
	}
	t.Logf("box pointer-free: %v", !hasPtr(reflect.TypeOf(box{})))
}
