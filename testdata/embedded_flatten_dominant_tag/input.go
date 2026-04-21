package embeddedflattendominanttag

// Two embedded types contribute the same JSON name "Name" at depth 1.
// A.Foo is tagged to that name; B.Name inherits the wire name from the
// Go field name (untagged). Per encoding/json's dominant-field rule,
// the tagged contribution wins.

type A struct {
	Foo int `json:"Name"`
}

type B struct {
	Name string
}

//gents:export
type Outer struct {
	A
	B
}
