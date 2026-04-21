package panicunknownjsonflag

//gents:export
type Foo struct {
	N int `json:"n,bogusflag"`
}
