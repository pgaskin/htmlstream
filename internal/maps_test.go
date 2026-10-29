package internal

import (
	"reflect"
	"testing"
)

func TestMapReset(t *testing.T) {
	var m Map[int, struct{}]
	m.Reset(4)
	id := func() uintptr { return reflect.ValueOf(m.M).Pointer() }
	small := id()
	for i := range 4 {
		m.M[i] = struct{}{}
	}
	m.Reset(4)
	if len(m.M) != 0 || id() != small {
		t.Errorf("small map not cleared and reused: %d entries", len(m.M))
	}
	for i := range 1000 {
		m.M[i] = struct{}{}
	}
	m.Reset(1000)
	if len(m.M) != 0 {
		t.Errorf("%d entries", len(m.M))
	}
	m.Reset(4)
	if id() == small {
		t.Errorf("map which held 1000 entries kept for 4")
	}
	if m.Reset(4); len(m.M) != 0 {
		t.Errorf("%d entries", len(m.M))
	}
}
