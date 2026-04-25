// Package gents generates TypeScript interfaces and factory functions from
// Go struct definitions marked with a //gents:export doc comment.
//
// See docs/gents_package.md for the authoritative design reference.
package gents

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Options tunes emission. The zero value emits verbatim names with no
// factory stripping — gents takes no position on Go naming convention,
// so library callers stay explicit about whether they want a prefix
// stripped. The CLI default is `-strip=t`, but the library API does not
// inject defaults of its own.
type Options struct {
	// Strip is the prefix removed from Go struct names when building the
	// factory function name (e.g. Strip="t" turns the marked struct
	// `tFoo` into the factory `newFoo()`). The TS interface name itself
	// is always emitted verbatim from the Go struct name — Strip never
	// touches it. Empty string (the default) means no stripping; the
	// factory for `tFoo` would be `newtFoo()`.
	Strip string

	// TypeMap supplies user-defined Go-to-TS type mappings. Keys are Go
	// type identifiers ("MyString", "uuid.UUID") matching *ast.Ident
	// names or package-qualified *ast.SelectorExpr strings. Values are
	// TS type expressions (e.g. "string", "Date", "string | null",
	// "Record<string, number>"). Factory zero values are inferred from
	// the TS type; types whose zero cannot be inferred panic at emit
	// time with a message pointing at the offending field.
	//
	// User mappings take precedence over built-ins, so
	// TypeMap["time.Time"] = "Date" is how you override the default
	// string mapping.
	TypeMap map[string]string
}

// Generate parses a single Go source file, walks every //gents:export-marked
// struct, and returns the TypeScript source. An empty string with a nil
// error means the input had no marked structs.
//
// Panics on unsupported Go types or malformed input. Callers that need
// panics converted to errors should recover themselves.
func Generate(inPath string, opts Options) (string, error) {
	return generate([]string{inPath}, opts)
}

// GenerateDir is the bundle-mode entry point: it walks dirPath recursively,
// finds every .go file (excluding _test.go and files starting with _ or .),
// parses them together, and emits a single TypeScript source covering every
// //gents:export-marked struct across the tree. Cross-file references
// resolve naturally because all marked structs share one output.
//
// Name collisions (two different Go structs that map to the same TS name
// after stripping) panic with a message identifying both sources.
func GenerateDir(dirPath string, opts Options) (string, error) {
	paths, err := collectGoFiles(dirPath)
	if err != nil {
		return "", err
	}
	return generate(paths, opts)
}

// generate is the core: parse every path with a shared fset, run Pass 1
// across all files to build one unified marked-struct map, then Pass 2 in
// file-then-source order to collect fields. Emission is deterministic
// because paths arrive sorted and struct/field order within each file is
// preserved.
func generate(paths []string, opts Options) (string, error) {
	fset := token.NewFileSet()
	e := &emitter{
		fset:           fset,
		marked:         map[string]string{},
		origin:         map[string]token.Pos{},
		strip:          opts.Strip,
		directiveMap:   map[string]directiveOriginPos{},
		namedAliases:   map[string]ast.Expr{},
		allStructs:     map[string]*ast.StructType{},
		hasMarshalJSON: map[string]bool{},
		resolving:      map[string]bool{},
		visiting:       map[string]bool{},
	}

	files := make([]*ast.File, 0, len(paths))
	for _, p := range paths {
		f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if err != nil {
			return "", fmt.Errorf("parse %s: %w", p, err)
		}
		files = append(files, f)
		e.collectMarked(f)
		e.collectAuxInfo(f)
		e.collectDirectives(f)
	}

	// Merge type mappings: directives form the base; CLI Options.TypeMap
	// overrides silently (an explicit runtime flag is the caller's final
	// word). Conflicts between directives themselves already panicked in
	// collectDirectives.
	merged := make(map[string]string, len(e.directiveMap)+len(opts.TypeMap))
	for k, v := range e.directiveMap {
		merged[k] = v.value
	}
	for k, v := range opts.TypeMap {
		merged[k] = v
	}
	e.typeMap = merged
	e.checkTypeMapCollisions()

	var all []structInfo
	for _, f := range files {
		all = append(all, e.collectStructs(f)...)
	}

	if len(all) == 0 {
		return "", nil
	}
	return e.emit(all), nil
}

// collectGoFiles walks dirPath recursively and returns all .go file paths,
// sorted alphabetically for deterministic processing order. Excludes
// _test.go files and paths containing a component starting with _ or .
// (matches go-build conventions). Errors if dirPath is not a directory.
func collectGoFiles(dirPath string) ([]string, error) {
	info, err := os.Stat(dirPath)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory (use -in for single-file mode)", dirPath)
	}

	var paths []string
	err = filepath.WalkDir(dirPath, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			// Skip nested `testdata`, `_`-prefixed, and `.`-prefixed
			// directories. Matches the Go toolchain convention
			// (`go test ./...` etc). Only applied to nested dirs, so
			// pointing -in directly at e.g. ./testdata still works.
			if p != dirPath && (name == "testdata" || strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		if strings.HasSuffix(name, "_test.go") {
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

// collectMarked is pass 1: record every marked struct's original Go name
// and its stripped factory base name. Enforces two uniqueness invariants
// across all files processed so far: (1) no two marked structs may share
// the same Go name (would collide on the verbatim TS interface name), and
// (2) no two marked structs may map to the same factory base name after
// stripping (would collide on the emitted "newX" factory). Either
// collision panics with file:line pointing at the later definition.
func (e *emitter) collectMarked(file *ast.File) {
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		groupMarked := hasMarker(gd.Doc)
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if !(groupMarked || hasMarker(ts.Doc)) {
				continue
			}
			if _, isStruct := ts.Type.(*ast.StructType); !isStruct {
				kind := "non-struct type"
				if ts.Assign != token.NoPos {
					kind = "type alias"
				}
				e.panicAt(ts.Pos(),
					"//gents:export is only supported on struct types; %q is a %s",
					ts.Name.Name, kind)
			}
			orig := ts.Name.Name
			factory := stripPrefix(orig, e.strip)

			if prevPos, exists := e.origin[orig]; exists {
				e.panicAt(ts.Pos(),
					"duplicate //gents:export struct %q (previously defined at %s)",
					orig, e.fset.Position(prevPos))
			}
			for otherOrig, otherFactory := range e.marked {
				if otherFactory == factory {
					e.panicAt(ts.Pos(),
						"factory name collision: %q strips to %q which conflicts with %q defined at %s",
						orig, factory, otherOrig, e.fset.Position(e.origin[otherOrig]))
				}
			}
			e.marked[orig] = factory
			e.origin[orig] = ts.Pos()
		}
	}
}

// collectStructs is pass 2: walk declarations a second time in source order
// and, for every struct marked during pass 1, collect its field metadata.
func (e *emitter) collectStructs(file *ast.File) []structInfo {
	var out []structInfo
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			orig := ts.Name.Name
			factory, ok := e.marked[orig]
			if !ok {
				continue
			}
			st := ts.Type.(*ast.StructType)
			out = append(out, structInfo{
				origName:    orig,
				factoryBase: factory,
				fields:      e.collectFields(st, orig),
			})
		}
	}
	return out
}
