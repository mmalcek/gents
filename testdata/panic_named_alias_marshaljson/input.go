package panicnamedaliasmarshaljson

import "encoding/json"

// Special has a custom MarshalJSON — the wire shape doesn't match the
// underlying string. Auto-resolution would silently emit the wrong TS
// type, so gents panics and directs the user to -map.

type Special string

func (s Special) MarshalJSON() ([]byte, error) {
	return json.Marshal("custom-" + string(s))
}

//gents:export
type Foo struct {
	Value Special `json:"value"`
}
