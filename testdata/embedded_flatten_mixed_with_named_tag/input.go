package embeddedflattenmixed

// Three embedded-field patterns in a single struct:
//   - Flat (no tag)         → flattened into Foo at depth 1
//   - Nested `json:"nested"` → emitted as a single nested field
//   - Skipped `json:"-"`     → dropped entirely

type Flat struct {
	A string `json:"a"`
}

//gents:export
type Nested struct {
	B string `json:"b"`
}

type Skipped struct {
	C string `json:"c"`
}

//gents:export
type Foo struct {
	Flat
	Nested  `json:"nested"`
	Skipped `json:"-"`
	Name    string `json:"name"`
}
