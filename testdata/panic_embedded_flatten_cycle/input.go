package panicembeddedflattencycle

// A embeds *A — legal Go (pointers break the size cycle), but flattening
// would recurse forever. The visiting-set cycle detector fires on
// re-entry into A.

type A struct {
	*A
	Name string `json:"name"`
}

//gents:export
type Foo struct {
	A
}
