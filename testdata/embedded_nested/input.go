package embeddednested

//gents:export
type Base struct {
	X int `json:"x"`
}

//gents:export
type Foo struct {
	Base `json:"base"`
	Y    int `json:"y"`
}
