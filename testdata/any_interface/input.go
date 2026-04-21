package anyinterface

//gents:export
type Foo struct {
	Anything any         `json:"anything"`
	Iface    interface{} `json:"iface"`
}
