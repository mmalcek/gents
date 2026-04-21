package api

import (
	"time"

	"github.com/google/uuid"
)

//gents:export
type User struct {
	ID      uuid.UUID `json:"id"`
	Created time.Time `json:"created"`
}
