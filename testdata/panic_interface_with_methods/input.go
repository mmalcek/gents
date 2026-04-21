package panicinterfacewithmethods

//gents:export
type Foo struct {
	I interface{ Do() } `json:"i"`
}
