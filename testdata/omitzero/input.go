package omitzero

import "time"

//gents:export
type Event struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at,omitzero"`
	Retries   int       `json:"retries,omitzero"`
	Label     string    `json:"label,omitempty"`
}
