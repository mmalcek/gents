package panicunnamedstruct

//gents:export
type Foo struct {
	Nested struct{ X int } `json:"nested"`
}
