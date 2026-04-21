package typemapoverridebuiltin

import "time"

//gents:export
type Event struct {
	When time.Time `json:"when"`
}
