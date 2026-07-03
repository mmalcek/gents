package jsdoccomments

// Area is a physical site area.
// Categories are denormalized for display.
//
//gents:export
type Area struct {
	ID string `json:"id"`
	// IANA timezone name.
	// Defaults from global settings.
	Timezone string `json:"timezone"`
	Note     string `json:"note"` // free-form note
}

//gents:export
type Bare struct {
	A string `json:"a"`
}
