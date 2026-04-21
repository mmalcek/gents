package embeddedskipped

type Base struct {
	Secret string `json:"secret"`
}

//gents:export
type Foo struct {
	Base `json:"-"`
	Y    int `json:"y"`
}
