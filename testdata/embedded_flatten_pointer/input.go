package embeddedflattenpointer

type Base struct {
	ID string `json:"id"`
	N  int    `json:"n"`
}

//gents:export
type Foo struct {
	*Base
	Name string `json:"name"`
}
