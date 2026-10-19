package prefs

import "reflect"

// clone returns a copy of v that shares no memory with it: pointers, maps,
// slices and interface values are copied recursively. Fields that are not
// exported are copied by value (a pointer or map hidden in one stays shared);
// encoding/json ignores such fields unless the type has its own marshaler.
func clone[T any](v T) T {
	var out T
	deepCopy(reflect.ValueOf(&out).Elem(), reflect.ValueOf(&v).Elem(), 0)
	return out
}

// hasRefs reports whether t can hold memory that a plain assignment would share.
func hasRefs(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface, reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return true
	case reflect.Array:
		return hasRefs(t.Elem())
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if hasRefs(t.Field(i).Type) {
				return true
			}
		}
	}
	return false
}

const maxCloneDepth = 1000 // encoding/json gives up on deeper (cyclic) values too

func deepCopy(dst, src reflect.Value, depth int) {
	if depth > maxCloneDepth {
		dst.Set(src)
		return
	}
	switch src.Kind() {
	case reflect.Pointer:
		if src.IsNil() {
			return
		}
		n := reflect.New(src.Type().Elem())
		deepCopy(n.Elem(), src.Elem(), depth+1)
		dst.Set(n)
	case reflect.Interface:
		if src.IsNil() {
			return
		}
		e := src.Elem()
		n := reflect.New(e.Type()).Elem()
		deepCopy(n, e, depth+1)
		dst.Set(n)
	case reflect.Map:
		if src.IsNil() {
			return
		}
		m := reflect.MakeMapWithSize(src.Type(), src.Len())
		for it := src.MapRange(); it.Next(); {
			k := reflect.New(src.Type().Key()).Elem()
			deepCopy(k, it.Key(), depth+1)
			v := reflect.New(src.Type().Elem()).Elem()
			deepCopy(v, it.Value(), depth+1)
			m.SetMapIndex(k, v)
		}
		dst.Set(m)
	case reflect.Slice:
		if src.IsNil() {
			return
		}
		s := reflect.MakeSlice(src.Type(), src.Len(), src.Len())
		for i := 0; i < src.Len(); i++ {
			deepCopy(s.Index(i), src.Index(i), depth+1)
		}
		dst.Set(s)
	case reflect.Array:
		for i := 0; i < src.Len(); i++ {
			deepCopy(dst.Index(i), src.Index(i), depth+1)
		}
	case reflect.Struct:
		dst.Set(src) // the unexported fields, by value
		for i := 0; i < src.NumField(); i++ {
			if f := dst.Field(i); f.CanSet() {
				deepCopy(f, src.Field(i), depth+1)
			}
		}
	default:
		dst.Set(src)
	}
}
