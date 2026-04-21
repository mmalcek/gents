package panicembeddedflattenforeign

import "time"

// Embedding a type from another package (qualified selector) cannot be
// resolved statically — gents doesn't load external packages. Panic
// with a pointer at the cross-package limitation and the -map
// workaround.

//gents:export
type Foo struct {
	time.Time
	Name string `json:"name"`
}
