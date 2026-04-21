package timeandpointers

import "time"

//gents:export
type Foo struct {
	Created time.Time  `json:"created"`
	Updated *time.Time `json:"updated"`
	Name    *string    `json:"name"`
	Scores  []int      `json:"scores"`
}
