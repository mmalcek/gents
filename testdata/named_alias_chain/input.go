package namedaliaschain

type A B
type B C
type C string

//gents:export
type Foo struct {
	Value A `json:"value"`
}
