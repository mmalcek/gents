package panicembedded

type Base struct {
	X int `json:"x"`
}

//gents:export
type Foo struct {
	Base
	Y int `json:"y"`
}
