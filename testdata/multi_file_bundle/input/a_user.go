package api

//gents:export
type User struct {
	ID      string   `json:"id"`
	Profile Profile `json:"profile"`
}
