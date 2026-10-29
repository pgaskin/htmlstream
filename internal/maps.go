package internal

// IndexMin is the number of attributes or namespace declarations above which
// they are looked up in a map rather than by a linear search, so that reading
// a tag takes time linear in the number of its attributes.
const IndexMin = 16

// Map is a map reused for each tag. Emptying a map with clear takes time
// proportional to the most entries it has held, so Reset replaces it after it
// was used for a much larger tag.
type Map[K comparable, V any] struct {
	M    map[K]V
	peak int // the most entries M has held when it was emptied
}

// Reset empties the map for at most n entries. The map isn't allocated for n
// entries, since tags often repeat names (e.g., duplicate attributes).
func (m *Map[K, V]) Reset(n int) {
	m.peak = max(m.peak, len(m.M))
	if m.M == nil || m.peak > 4*n+64 {
		m.M, m.peak = make(map[K]V), 0
		return
	}
	clear(m.M)
}
