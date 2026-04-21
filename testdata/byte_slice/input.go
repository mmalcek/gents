package byteslice

//gents:export
type Foo struct {
	Data  []byte  `json:"data"`
	Maybe *[]byte `json:"maybe"`
	Bytes []uint8 `json:"bytes"`
}
