package jsonrawmessage

import "encoding/json"

//gents:export
type Foo struct {
	Payload json.RawMessage `json:"payload"`
}
