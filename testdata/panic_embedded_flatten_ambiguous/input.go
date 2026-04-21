package panicembeddedflattenambiguous

// A and B both contribute a tagged "x" at depth 1 when flattened into
// Foo. Dominant-field resolution can't pick a winner, so gents panics.

type A struct {
	X int `json:"x"`
}

type B struct {
	X int `json:"x"`
}

//gents:export
type Foo struct {
	A
	B
}
