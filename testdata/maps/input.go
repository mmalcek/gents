package maps

//gents:export
type Foo struct {
	Tags  map[string]string `json:"tags"`
	Items map[string]Item   `json:"items"`
	Flags map[string]*bool  `json:"flags"`
}

//gents:export
type Item struct {
	Value string `json:"value"`
}
