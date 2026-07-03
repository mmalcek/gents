package constexport

// Permission bits for access profiles.
//
//gents:export
const (
	PermRead   = 1 // view or list records
	PermCreate = 2 // create records
	PermUpdate = 4
	PermDelete = 8
	PermAll    = PermRead | PermCreate | PermUpdate | PermDelete
)

//gents:export
const (
	// Role assigned when no profile matches.
	DefaultRole = "guest"
	MaxItems    = 1 << 10
	Threshold   = -0.5
	LegacyOctal = 017
	FeatureOn   = true
	Combined    = (PermRead | PermCreate) & PermAll
)
