package panicunknownnamedtype

// SomeUnknownType is referenced but not declared in this input.
// Auto-resolution can't find it; TypeMap doesn't cover it; the user
// probably forgot to define or import it.

//gents:export
type Foo struct {
	Name SomeUnknownType `json:"name"`
}
