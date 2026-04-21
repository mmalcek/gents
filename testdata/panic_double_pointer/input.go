package panicdoublepointer

//gents:export
type Foo struct {
	P **string `json:"p"`
}
