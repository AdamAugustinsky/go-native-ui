package ui

import (
	"reflect"
	"testing"

	"gonativeui/gpu"
)

func TestContextInitializedByValueKeepsStyles(t *testing.T) {
	draw := func(c *Context) gpu.Quad {
		t.Helper()
		c.Begin(&Input{}, 400, 300, 1, 1.0/60)
		c.NextSize(Px(75), Px(25))
		c.NextBG(0x112233FF)
		c.Box(DrawBackground, "box")
		var list gpu.DrawList
		c.End(&list)
		if len(list.Quads) != 1 {
			t.Fatalf("got %d quads, want one styled box", len(list.Quads))
		}
		return list.Quads[0]
	}
	want := draw(New(newFake()))
	c := *New(newFake())
	if got := draw(&c); got != want {
		t.Fatalf("value-initialized Context drew %+v, want %+v", got, want)
	}
}

func TestBoxFieldsHaveExplicitLifetime(t *testing.T) {
	typ := reflect.TypeOf(box{})
	if typ.NumField() != 2 {
		t.Fatal("box must only embed persist and boxFrame; put new persistent fields in persist and per-frame fields in boxFrame")
	}
	for i, want := range []reflect.Type{reflect.TypeOf(persist{}), reflect.TypeOf(boxFrame{})} {
		field := typ.Field(i)
		if !field.Anonymous || field.Type != want {
			t.Fatalf("box field %s must embed %s; put new fields inside persist or boxFrame according to their lifetime", field.Name, want.Name())
		}
	}
}
