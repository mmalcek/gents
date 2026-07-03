package typemapfactorybaseok

// tstring strips (with -strip=t) to the factory base name "string". A
// user mapping to TS "string" must NOT be reported as a collision: the
// emitted interface is named "tstring", and factory names live in value
// space where type expressions cannot collide.
//
//gents:export
type tstring struct {
	Value string `json:"value"`
}

//gents:export
type tItem struct {
	ID MyID `json:"id"`
}
