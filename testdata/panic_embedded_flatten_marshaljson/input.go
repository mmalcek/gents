package panicembeddedflattenmarshaljson

import "encoding/json"

// Base has a custom MarshalJSON — flattening would produce the wrong
// wire shape because Go's encoding/json delegates to MarshalJSON rather
// than walking the fields. gents must refuse to flatten.

type Base struct {
	ID string `json:"id"`
}

func (b Base) MarshalJSON() ([]byte, error) {
	return json.Marshal("custom-" + b.ID)
}

//gents:export
type Foo struct {
	Base
	Name string `json:"name"`
}
