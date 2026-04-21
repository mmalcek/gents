package namedaliasresolved

type UserID string
type Score int
type Active bool

//gents:export
type User struct {
	ID     UserID `json:"id"`
	Score  Score  `json:"score"`
	Active Active `json:"active"`
}
