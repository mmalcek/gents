package panicembeddedflattennotstruct

// Embedding a named primitive / type alias is legal Go but gents only
// flattens struct-embedded fields. Panic with the "not a struct type"
// message pointing at the workarounds.

type Label string

//gents:export
type Foo struct {
	Label
	Name string `json:"name"`
}
