package embeddedflattendominantname

// Base.X is shadowed by Foo.X (the outer struct's depth-0 field wins
// over the embedded type's depth-1 contribution).

type Base struct {
	X int `json:"x"`
}

//gents:export
type Foo struct {
	Base
	X    string `json:"x"`
	Name string `json:"name"`
}
