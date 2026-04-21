package panicunmarkedsiblingref

type Inner struct {
	V string `json:"v"`
}

//gents:export
type Outer struct {
	Inner Inner `json:"inner"`
}
