package constcollidesinterface

// Go itself would reject this package (duplicate top-level name), but
// gents parses without type-checking and must catch it on its own.

//gents:export
type Foo struct {
	A string `json:"a"`
}

//gents:export
const Foo = 1
