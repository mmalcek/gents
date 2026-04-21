package panicnamedaliascycle

// Go's compiler would reject this as an invalid recursive type, but
// gents parses without type-checking and must defend against the cycle
// during auto-resolution.

type A B
type B A

//gents:export
type Foo struct {
	Value A `json:"value"`
}
