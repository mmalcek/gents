package crossstructref

//gents:export
type Outer struct {
	Inner Inner `json:"inner"`
}

//gents:export
type Inner struct {
	Value string `json:"value"`
}
