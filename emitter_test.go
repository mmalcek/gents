package gents_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmalcek/gents"
)

// goldenCase covers both single-file and bundle-mode fixtures. When isDir
// is true the fixture has an `input/` directory scanned recursively;
// otherwise the fixture has a single `input.go` file.
type goldenCase struct {
	dir   string
	isDir bool
	opts  gents.Options
}

var goldenCases = []goldenCase{
	{"simple", false, gents.Options{}},
	{"cross_struct_ref", false, gents.Options{}},
	{"json_tag_variants", false, gents.Options{}},
	{"time_and_pointers", false, gents.Options{}},
	{"maps", false, gents.Options{}},
	{"byte_slice", false, gents.Options{}},
	{"time_duration", false, gents.Options{}},
	{"json_raw_message", false, gents.Options{}},
	{"any_interface", false, gents.Options{}},
	{"grouped_type_decl", false, gents.Options{}},
	{"quoted_field_names", false, gents.Options{}},
	{"empty_struct", false, gents.Options{}},
	{"all_omitempty", false, gents.Options{}},
	{"custom_strip", false, gents.Options{Strip: "c"}},
	{"verbatim_strip", false, gents.Options{}},
	{"json_string_flag", false, gents.Options{}},
	{"omitzero", false, gents.Options{}},
	{"type_map_named_alias", false, gents.Options{TypeMap: map[string]string{"MyString": "string"}}},
	{"type_map_qualified", false, gents.Options{TypeMap: map[string]string{
		"uuid.UUID":       "string",
		"decimal.Decimal": "string",
	}}},
	{"type_map_override_builtin", false, gents.Options{TypeMap: map[string]string{"time.Time": "Date | null"}}},
	{"embedded_nested", false, gents.Options{}},
	{"embedded_skipped", false, gents.Options{}},
	{"named_alias_resolved", false, gents.Options{}},
	{"named_alias_chain", false, gents.Options{}},
	{"multi_file_bundle", true, gents.Options{}},
	{"multi_file_nested", true, gents.Options{}},
}

func runGenerate(c goldenCase) (string, error) {
	if c.isDir {
		return gents.GenerateDir(filepath.Join("testdata", c.dir, "input"), c.opts)
	}
	return gents.Generate(filepath.Join("testdata", c.dir, "input.go"), c.opts)
}

func TestGenerateMatchesGolden(t *testing.T) {
	for _, c := range goldenCases {
		t.Run(c.dir, func(t *testing.T) {
			got, err := runGenerate(c)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			want, err := os.ReadFile(filepath.Join("testdata", c.dir, "expected.ts"))
			if err != nil {
				t.Fatalf("read expected.ts: %v", err)
			}
			if got != string(want) {
				t.Fatalf("output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}

// TestIdempotent catches nondeterministic output (the usual suspect is
// iterating over a map somewhere in the emitter). We run each fixture N
// times and assert every run matches the first.
func TestIdempotent(t *testing.T) {
	const runs = 10
	for _, c := range goldenCases {
		t.Run(c.dir, func(t *testing.T) {
			first, err := runGenerate(c)
			if err != nil {
				t.Fatalf("Generate (run 1): %v", err)
			}
			for i := 2; i <= runs; i++ {
				got, err := runGenerate(c)
				if err != nil {
					t.Fatalf("Generate (run %d): %v", i, err)
				}
				if got != first {
					t.Fatalf("run %d differs from run 1", i)
				}
			}
		})
	}
}

func TestUnmarkedProducesEmpty(t *testing.T) {
	got, err := gents.Generate(filepath.Join("testdata", "unmarked_skip", "input.go"), gents.Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty output, got %q", got)
	}
}

// TestBlockCommentMarkerNotRecognized — the marker must be a //-style
// line comment, not a block comment.
func TestBlockCommentMarkerNotRecognized(t *testing.T) {
	dir := t.TempDir()
	src := `package blockcomment

/* gents:export */
type tFoo struct {
	A string ` + "`json:\"a\"`" + `
}
`
	path := filepath.Join(dir, "input.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := gents.Generate(path, gents.Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got != "" {
		t.Fatalf("block-comment marker should NOT mark the struct; got output:\n%s", got)
	}
}

// Panic tests — cover both single-file and bundle-mode panic fixtures.
type panicCase struct {
	dir      string
	isDir    bool
	contains string
	opts     gents.Options
}

var panicCases = []panicCase{
	{"panic_chan", false, "channel", gents.Options{}},
	{"panic_embedded", false, "flattening is not yet supported", gents.Options{}},
	{"panic_map_nonstring_key", false, "string-keyed", gents.Options{}},
	{"panic_array", false, "fixed-length", gents.Options{}},
	{"panic_double_pointer", false, "double pointer", gents.Options{}},
	{"panic_unnamed_struct", false, "inline anonymous", gents.Options{}},
	{"panic_unknown_named_type", false, "unsupported named type", gents.Options{}},
	{"panic_unknown_json_flag", false, "unsupported json tag flag", gents.Options{}},
	{"panic_string_flag_on_slice", false, "only valid on numeric or boolean", gents.Options{}},
	{"panic_string_flag_on_string", false, "already string", gents.Options{}},
	{"panic_interface_with_methods", false, "interface types with methods", gents.Options{}},
	{"panic_unmarked_sibling_ref", false, "unsupported named type", gents.Options{}},
	{"panic_multi_origname_collision", true, "duplicate", gents.Options{}},
	// tsname collision only surfaces when stripping maps two different Go names to the same TS name.
	{"panic_multi_tsname_collision", true, "TS name collision", gents.Options{Strip: "t"}},
	// typemap collision: mapped TS name equals a generated interface name.
	{"panic_type_map_collision", false, "collides with the generated interface", gents.Options{TypeMap: map[string]string{"Something": "User"}}},
	// typemap with zero-inference failure: user maps a type we can't produce a zero for.
	{"panic_type_map_uninferrable", false, "cannot infer factory zero value", gents.Options{TypeMap: map[string]string{"time.Time": "Date"}}},
	// //gents:export is only valid on struct types — not on aliases / interfaces / named non-structs.
	{"panic_marker_on_alias", false, "type alias", gents.Options{}},
	{"panic_marker_on_interface", false, "non-struct type", gents.Options{}},
	{"panic_named_alias_cycle", false, "cycle in type-alias resolution", gents.Options{}},
	{"panic_named_alias_marshaljson", false, "MarshalJSON", gents.Options{}},
}

func TestPanics(t *testing.T) {
	for _, c := range panicCases {
		t.Run(c.dir, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("expected panic, got none")
				}
				err, ok := r.(error)
				if !ok {
					t.Fatalf("panic payload is %T, want error; payload=%v", r, r)
				}
				msg := err.Error()
				if !strings.Contains(msg, c.contains) {
					t.Fatalf("panic message %q does not contain %q", msg, c.contains)
				}
				if !strings.Contains(msg, ".go:") {
					t.Fatalf("panic message %q missing file:line pointer", msg)
				}
			}()
			if c.isDir {
				_, _ = gents.GenerateDir(filepath.Join("testdata", c.dir, "input"), c.opts)
			} else {
				_, _ = gents.Generate(filepath.Join("testdata", c.dir, "input.go"), c.opts)
			}
		})
	}
}

// TestGenerateDirEmpty — a directory with no marked structs returns "".
func TestGenerateDirEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte("package empty\n\ntype tFoo struct{}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := gents.GenerateDir(dir, gents.Options{})
	if err != nil {
		t.Fatalf("GenerateDir: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty output, got %q", got)
	}
}

// TestGenerateDirNotADirectory — passing a file to GenerateDir errors.
func TestGenerateDirNotADirectory(t *testing.T) {
	_, err := gents.GenerateDir(filepath.Join("testdata", "simple", "input.go"), gents.Options{})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestGenerateParseError — malformed Go should surface a parse error
// (not a panic, and not a confusing unrelated failure).
func TestGenerateParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.go")
	if err := os.WriteFile(path, []byte("package x\n::garbage::\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := gents.Generate(path, gents.Options{})
	if err == nil {
		t.Fatalf("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Fatalf("error should reference parse failure, got %v", err)
	}
}
