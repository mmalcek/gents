package typemapnamedalias

type MyString string

//gents:export
type Foo struct {
	Name MyString `json:"name"`
}
