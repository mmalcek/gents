package embeddedflattenchain

type Core struct {
	A string `json:"a"`
}

type Bar struct {
	Core
	B string `json:"b"`
}

//gents:export
type Foo struct {
	Bar
	C string `json:"c"`
}
