package jsontagvariants

//gents:export
type Foo struct {
	Required  string `json:"required"`
	Optional  string `json:"optional,omitempty"`
	Hidden    string `json:"-"`
	BareName  string `json:",omitempty"`
	NoTag     string
	EmptyName string `json:""`
}
