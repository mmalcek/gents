package panictypemapuninferrable

import "time"

//gents:export
type Foo struct {
	When time.Time `json:"when"`
}
