//gents:map uuid.UUID=number

package x

import "github.com/google/uuid"

//gents:export
type Foo struct {
	ID uuid.UUID `json:"id"`
}
