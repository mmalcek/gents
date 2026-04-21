package timeduration

import "time"

//gents:export
type Foo struct {
	Timeout time.Duration `json:"timeout"`
}
