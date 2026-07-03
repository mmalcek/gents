package tstagwithstringflag

//gents:export
type Foo struct {
	N int64 `json:"n,string" ts:"string"`
}
