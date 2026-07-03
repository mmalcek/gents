package gents

import (
	"fmt"
	"go/ast"
	"go/token"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// typeInfo pairs a TypeScript type expression with the factory zero value
// literal for that type. Everything flowing through mapGoType is a typeInfo.
type typeInfo struct {
	ts   string
	zero string
}

// fieldInfo describes one emitted field on a struct. jsonName is the raw
// wire name (the spec refers to it as the JSON name); tsName is the
// emission-ready form (bare ident or 'quoted' form) used both in the
// interface property and the factory object-literal key. depth, pos and
// tagged are used only during dominant-field resolution for embedded
// flattening (§3.2) and are ignored by the emission phase.
type fieldInfo struct {
	jsonName string
	tsName   string
	optional bool
	ti       typeInfo
	doc      []string  // cleaned doc-comment lines carried through to a JSDoc block
	depth    int       // 0 = directly on the outer struct; 1+ = contributed via embedding
	pos      token.Pos // source position of the contributing Go field (for diagnostics)
	tagged   bool      // true when jsonName came from a json:"..." tag with a non-empty name
}

// structInfo carries everything emit() needs for one marked struct.
// origName is the Go struct name and is used verbatim as the TS interface
// name and as the type expression in cross-struct references — gents takes
// no position on Go naming convention. factoryBase is the Go name with the
// configured prefix stripped; it's only used to build the "newX" factory
// function name, where the JS-side convention `newFoo()` reads better
// without the Go-only `t`-style prefix.
type structInfo struct {
	origName    string
	factoryBase string
	doc         []string // cleaned doc-comment lines emitted as JSDoc above the interface
	src         string   // base filename of the declaring Go file, for the (source: ...) line
	fields      []fieldInfo
}

// constInfo is one emitted `export const` line. value is the already
// rendered TS expression.
type constInfo struct {
	name  string
	value string
	doc   []string
}

// constBlock groups the consts contributed by one marked `const (...)`
// declaration. Blocks emit in scan order, before any interface.
type constBlock struct {
	doc    []string
	src    string
	consts []constInfo
}

type emitter struct {
	fset           *token.FileSet
	marked         map[string]string    // original Go name -> stripped factory base name
	origin         map[string]token.Pos // original Go name -> position of first definition (for collision diagnostics)
	constNames     map[string]token.Pos // exported const name -> position (duplicate detection + reference resolution)
	strip          string
	typeMap        map[string]string             // final merged Go-to-TS mappings (directives + CLI overrides)
	directiveMap   map[string]directiveOriginPos // mappings collected from //gents:map directives across all scanned files
	namedAliases   map[string]ast.Expr           // in-file non-struct type decls for auto-resolution (e.g. `type UserID string`)
	allStructs     map[string]*ast.StructType    // every top-level struct declaration in the scanned input, marked or not (powers embedded flattening)
	hasMarshalJSON map[string]bool               // types that declare a MarshalJSON method in the scanned input
	resolving      map[string]bool               // active alias-resolution set (cycle detection)
	visiting       map[string]bool               // active embedded-flatten descent set (cycle detection)
}

// directiveOriginPos records where a //gents:map directive lived, so
// conflict errors can point at both sides.
type directiveOriginPos struct {
	value string
	pos   token.Pos
}

// ---------------------------------------------------------------------------
// Marker + name utilities

// hasMarker reports whether the given comment group contains the exact
// line comment //gents:export — strict match, no leading/trailing
// whitespace, no trailing content, block comments never match.
func hasMarker(cg *ast.CommentGroup) bool {
	if cg == nil {
		return false
	}
	for _, c := range cg.List {
		if !strings.HasPrefix(c.Text, "//") {
			continue
		}
		if strings.TrimPrefix(c.Text, "//") == "gents:export" {
			return true
		}
	}
	return false
}

func stripPrefix(name, prefix string) string {
	if prefix != "" && strings.HasPrefix(name, prefix) {
		return name[len(prefix):]
	}
	return name
}

// lintMarkers panics on comments that were clearly meant to be gents
// directives but are silently ignored because of stray whitespace
// (`// gents:export`, `// gents:map A=B`). Prose that merely mentions a
// directive doesn't match: the export check requires the trimmed comment
// to be exactly the marker word, and the map check additionally requires
// a parseable GoType=TSType spec.
func (e *emitter) lintMarkers(file *ast.File) {
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			if !strings.HasPrefix(c.Text, "//") {
				continue
			}
			text := strings.TrimPrefix(c.Text, "//")
			trimmed := strings.TrimSpace(text)
			if trimmed == "gents:export" && text != "gents:export" {
				e.panicAt(c.Pos(),
					"found %q — the marker must be exactly //gents:export with no surrounding whitespace, otherwise it is silently ignored", c.Text)
			}
			if strings.HasPrefix(trimmed, "gents:map") && !strings.HasPrefix(text, "gents:map") {
				rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "gents:map"))
				if _, _, ok := parseMapSpec(rest); ok {
					e.panicAt(c.Pos(),
						"found %q — the directive must start exactly with //gents:map with no leading whitespace, otherwise it is silently ignored", c.Text)
				}
			}
		}
	}
}

// docLines converts a comment group into cleaned lines for JSDoc
// emission. ast.CommentGroup.Text already strips comment markers and
// directive lines (//gents:export, //gents:map), so only human-written
// prose survives. Returns nil when nothing survives.
func docLines(cg *ast.CommentGroup) []string {
	text := strings.TrimRight(cg.Text(), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// fieldDoc returns a field's doc lines: the comment above the field wins,
// a trailing same-line comment is the fallback.
func fieldDoc(field *ast.Field) []string {
	if doc := docLines(field.Doc); doc != nil {
		return doc
	}
	return docLines(field.Comment)
}

var tsIdentRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

var (
	tsStringLitRe = regexp.MustCompile(`^'(?:[^'\\]|\\.)*'$`)
	tsNumberLitRe = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)
)

// formatFieldName returns the field name suitable for emission into both an
// interface property list and an object literal. JSON names that aren't
// valid TS identifiers get wrapped in single quotes.
func formatFieldName(name string) string {
	if tsIdentRe.MatchString(name) {
		return name
	}
	return "'" + name + "'"
}

// wrapIfUnion adds parentheses around a TS type expression that contains a
// top-level union, so downstream suffixes (like []) bind correctly.
// E.g. (Foo | null)[] instead of Foo | null[].
func wrapIfUnion(ts string) string {
	if strings.Contains(ts, " | ") {
		return "(" + ts + ")"
	}
	return ts
}

// collectDirectives scans every comment in the file for `//gents:map
// GoType=TSType` directives and records them in e.directiveMap. Panics
// on malformed directives and on conflicting declarations across files.
// Directive mappings are global: a directive written in file A applies
// to references from file B in the same bundle — same semantics as the
// CLI `-map` flag.
func (e *emitter) collectDirectives(file *ast.File) {
	const prefix = "//gents:map"
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			if !strings.HasPrefix(c.Text, prefix) {
				continue
			}
			rest := strings.TrimSpace(c.Text[len(prefix):])
			if rest == "" {
				e.panicAt(c.Pos(), "//gents:map directive missing its spec (expected `//gents:map GoType=TSType`)")
			}
			goType, tsType, ok := parseMapSpec(rest)
			if !ok {
				e.panicAt(c.Pos(), "malformed //gents:map directive %q: expected `//gents:map GoType=TSType`", rest)
			}
			if existing, dup := e.directiveMap[goType]; dup && existing.value != tsType {
				e.panicAt(c.Pos(),
					"conflicting //gents:map for %q: %q here, %q at %s",
					goType, tsType, existing.value, e.fset.Position(existing.pos))
			}
			e.directiveMap[goType] = directiveOriginPos{value: tsType, pos: c.Pos()}
		}
	}
}

// parseMapSpec parses "GoType=TSType" — the shared format used by both
// the -map CLI flag and the //gents:map directive. Trims whitespace
// around each side and rejects empty keys / empty values.
func parseMapSpec(spec string) (goType, tsType string, ok bool) {
	idx := strings.Index(spec, "=")
	if idx <= 0 || idx == len(spec)-1 {
		return "", "", false
	}
	goType = strings.TrimSpace(spec[:idx])
	tsType = strings.TrimSpace(spec[idx+1:])
	if goType == "" || tsType == "" {
		return "", "", false
	}
	return goType, tsType, true
}

// collectAuxInfo records three things that power in-file auto-resolution
// and embedded flattening:
//
//  1. namedAliases — non-struct top-level type decls (e.g. `type UserID
//     string`). Stored as the RHS expression so mapIdent can recursively
//     map it later. Struct types are handled by collectMarked; anything
//     unmarked and non-struct goes here.
//
//  2. allStructs — every top-level struct declaration, marked or not,
//     keyed by its Go name. Embedded flattening resolves a target struct
//     through this map so unmarked Base types can be flattened into
//     marked outer structs without being exported themselves.
//
//  3. hasMarshalJSON — any type in the scanned input that declares a
//     MarshalJSON method. Safety net: if a named alias also has
//     MarshalJSON, auto-resolution would miss the custom wire shape, so
//     we panic with a hint to use -map instead.
func (e *emitter) collectAuxInfo(file *ast.File) {
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if stDecl, isStruct := ts.Type.(*ast.StructType); isStruct {
					// Record every struct declaration (marked or not).
					// Duplicate names across files are impossible for
					// marked structs — collectMarked panics on that —
					// and harmless for unmarked ones since Go itself
					// forbids duplicate package-level type names.
					if _, exists := e.allStructs[ts.Name.Name]; !exists {
						e.allStructs[ts.Name.Name] = stDecl
					}
					continue
				}
				// Record non-struct top-level types. If this name was
				// already marked (collectMarked panics on marker+non-
				// struct), we never reach here for marked specs; only
				// genuine unmarked aliases land in the map.
				if _, exists := e.namedAliases[ts.Name.Name]; !exists {
					e.namedAliases[ts.Name.Name] = ts.Type
				}
			}
		case *ast.FuncDecl:
			// Detect `func (X) MarshalJSON() ...` or `func (*X) MarshalJSON() ...`.
			if d.Name.Name != "MarshalJSON" || d.Recv == nil || len(d.Recv.List) == 0 {
				continue
			}
			recvType := d.Recv.List[0].Type
			if star, ok := recvType.(*ast.StarExpr); ok {
				recvType = star.X
			}
			if ident, ok := recvType.(*ast.Ident); ok {
				e.hasMarshalJSON[ident.Name] = true
			}
		}
	}
}

// diagError wraps a deliberate diagnostic raised via panicAt so the
// recover at the public API boundary (generate) can distinguish it from
// a genuine bug. Diagnostics become returned errors; anything else
// re-panics with its stack intact.
type diagError struct{ err error }

// panicAt aborts the current walk with a file:line-prefixed diagnostic.
// Internal use of panic keeps the deeply recursive collectors free of
// threaded error returns (the same pattern encoding/json uses); the
// public API never lets it escape — generate() recovers and returns it
// as an ordinary error.
func (e *emitter) panicAt(pos token.Pos, format string, args ...any) {
	position := e.fset.Position(pos)
	panic(diagError{fmt.Errorf("%s: %s", position, fmt.Sprintf(format, args...))})
}

// ---------------------------------------------------------------------------
// Custom type mappings

// resolveTypeMap looks up key (either a bare Go ident like "MyString" or a
// qualified selector like "uuid.UUID") in the user-supplied TypeMap. If
// found, infers the factory zero value from the TS expression and returns
// the resulting typeInfo. The pos argument is used to point panics at the
// offending field if zero inference fails.
func (e *emitter) resolveTypeMap(key string, pos token.Pos) (typeInfo, bool) {
	ts, ok := e.typeMap[key]
	if !ok {
		return typeInfo{}, false
	}
	zero, err := inferZero(ts)
	if err != nil {
		e.panicAt(pos, "%s (from -map %s=%s)", err.Error(), key, ts)
	}
	return typeInfo{ts: ts, zero: zero}, true
}

// inferZero produces a TS factory zero-value literal for a TS type
// expression. Handles the cases listed in §3.10 "Custom type mappings"
// of the design doc. Returns an error (no panic) for unsupported shapes;
// callers decide whether to panic.
func inferZero(ts string) (string, error) {
	ts = strings.TrimSpace(ts)
	switch ts {
	case "string":
		return "''", nil
	case "number":
		return "0", nil
	case "boolean":
		return "false", nil
	case "unknown":
		return "null", nil
	}
	// Any union that includes `null` as one of its arms → zero is null.
	// Tolerates both spaced (`X | null`) and tight (`X|null`) forms.
	if strings.Contains(ts, "null") {
		for _, part := range strings.Split(ts, "|") {
			if strings.TrimSpace(part) == "null" {
				return "null", nil
			}
		}
	}
	// Literal types and literal unions ('manual' | 'automatic', 0 | 1):
	// the first arm is the natural zero.
	first := strings.TrimSpace(strings.Split(ts, "|")[0])
	if tsStringLitRe.MatchString(first) || tsNumberLitRe.MatchString(first) {
		return first, nil
	}
	if strings.HasSuffix(ts, "[]") {
		return "[]", nil
	}
	if strings.HasPrefix(ts, "Record<") {
		return "{}", nil
	}
	// Named types (Date, Uint8Array, custom classes, arbitrary unions):
	// we can't produce a type-correct zero without knowing the type's
	// constructor. Force the user to express nullability explicitly
	// (e.g. "Date | null") rather than silently emitting a null that
	// violates the declared type.
	return "", fmt.Errorf("cannot infer factory zero value for TS type %q — for named/class-like types add \"| null\" to make the field nullable, or use the library API to supply a custom zero", ts)
}

// checkTypeMapCollisions panics if any user-mapped TS type name matches
// the name of an interface gents is about to emit. Interface names are
// the original Go struct names (emitted verbatim, never stripped), so the
// comparison is against the keys of e.marked — comparing against the
// stripped factory base names would miss real collisions and flag
// non-collisions whenever -strip is in play.
func (e *emitter) checkTypeMapCollisions() {
	keys := make([]string, 0, len(e.typeMap))
	for k := range e.typeMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, goType := range keys {
		tsType := e.typeMap[goType]
		if pos, exists := e.origin[tsType]; exists {
			e.panicAt(pos,
				"mapped TS type %q (from -map %s=%s) collides with the generated interface for %q",
				tsType, goType, tsType, tsType)
		}
	}
}

// ---------------------------------------------------------------------------
// JSON tag parsing

// parseJSONTag reads the `json:"..."` struct tag on a field and returns its
// decomposition. skip=true means the field has `json:"-"` and must not be
// emitted. hasTag=false means no json tag was present (caller falls back to
// the Go field name). stringFlag reports whether encoding/json's ,string
// modifier is present — caller coerces the TS type via applyStringFlag.
// Panics on any unrecognized flag.
func (e *emitter) parseJSONTag(field *ast.Field) (wireName string, optional, stringFlag, skip, hasTag bool) {
	if field.Tag == nil {
		return "", false, false, false, false
	}
	raw := strings.Trim(field.Tag.Value, "`")
	val, ok := reflect.StructTag(raw).Lookup("json")
	if !ok {
		return "", false, false, false, false
	}
	if val == "-" {
		return "", false, false, true, true
	}
	parts := strings.Split(val, ",")
	for _, flag := range parts[1:] {
		switch flag {
		case "omitempty", "omitzero":
			// From gents's wire-shape perspective these are identical: the
			// field may or may not appear in the JSON output, so the TS
			// field is optional and the factory omits it. Which Go values
			// trigger omission is Go's problem, not TypeScript's.
			optional = true
		case "string":
			stringFlag = true
		case "":
			// empty segment (e.g. "name,") — silently accepted
		default:
			e.panicAt(field.Tag.Pos(), "unsupported json tag flag %q (supported: omitempty, omitzero, string)", flag)
		}
	}
	return parts[0], optional, stringFlag, false, true
}

// parseTSTag reads the `ts:"..."` struct tag — a per-field override of
// the emitted TS type expression. Returns the raw value and whether the
// tag was present.
func parseTSTag(field *ast.Field) (string, bool) {
	if field.Tag == nil {
		return "", false
	}
	raw := strings.Trim(field.Tag.Value, "`")
	return reflect.StructTag(raw).Lookup("ts")
}

// resolveFieldType computes a field's typeInfo. A ts:"..." tag replaces
// the Go-derived type entirely (it even works on Go types gents couldn't
// map on its own — the tag is a per-field escape hatch), with the factory
// zero inferred from the TS expression. Without the tag, the type comes
// from mapGoType plus the optional ,string coercion. Combining ts and
// ,string panics: the override already dictates the final type, so the
// coercion could only contradict it.
func (e *emitter) resolveFieldType(field *ast.Field, stringFlag bool) typeInfo {
	tsOverride, hasTS := parseTSTag(field)
	if hasTS {
		if stringFlag {
			e.panicAt(field.Tag.Pos(), "ts tag and json ,string flag cannot be combined; the ts tag already replaces the emitted type")
		}
		tsOverride = strings.TrimSpace(tsOverride)
		if tsOverride == "" {
			e.panicAt(field.Tag.Pos(), "empty ts tag; either supply a TS type expression or remove the tag")
		}
		zero, err := inferZero(tsOverride)
		if err != nil {
			e.panicAt(field.Tag.Pos(), "%s (from ts:%q)", err.Error(), tsOverride)
		}
		return typeInfo{ts: tsOverride, zero: zero}
	}
	ti := e.mapGoType(field.Type)
	if stringFlag {
		ti = e.applyStringFlag(ti, field.Pos())
	}
	return ti
}

// applyStringFlag coerces a field's TS type to reflect encoding/json's
// ,string modifier — the on-wire value is a JSON string containing the
// encoded value, so the TS type must be string. Numeric and boolean base
// types coerce cleanly; everything else panics because either (a)
// encoding/json itself ignores the flag on that type, or (b) it produces
// double-encoded output that is rarely intended.
func (e *emitter) applyStringFlag(base typeInfo, pos token.Pos) typeInfo {
	switch base.ts {
	case "number":
		return typeInfo{"string", "'0'"}
	case "boolean":
		return typeInfo{"string", "'false'"}
	case "number | null", "boolean | null":
		return typeInfo{"string | null", "null"}
	case "string", "string | null":
		e.panicAt(pos, "json ,string flag is not supported on fields whose base TS type is already string (remove the flag)")
	}
	e.panicAt(pos, "json ,string flag is only valid on numeric or boolean fields (got TS type %q)", base.ts)
	return typeInfo{}
}

// ---------------------------------------------------------------------------
// Field collection

// collectFields is the entry point: it walks the struct's fields and
// returns the resolved, in-order list to emit. For structs with embedded
// (anonymous) fields this is a two-pass process — collectFieldsDeep
// produces a flat slice whose entries carry their contributing depth,
// then resolveDominantFields applies encoding/json's least-nested and
// tagged-wins rules to eliminate shadowed entries.
func (e *emitter) collectFields(st *ast.StructType, origName string) []fieldInfo {
	var raw []fieldInfo
	e.collectFieldsDeep(st, 0, false, &raw)
	return e.resolveDominantFields(raw, origName)
}

// collectFieldsDeep walks st's fields and appends every contributing
// fieldInfo to out. For embedded fields it handles the three paths from
// §3.2: json:"-" skips, json:"name" nests the embedded type under that
// key (single entry at the current depth), and an untagged embed
// recursively flattens the target struct's fields at depth+1. The
// pointerEmbedded flag propagates downward: once set it forces every
// field contributed from the current descent onward to be optional,
// mirroring encoding/json's "nil pointer omits the embedded fields"
// behavior.
func (e *emitter) collectFieldsDeep(st *ast.StructType, depth int, pointerEmbedded bool, out *[]fieldInfo) {
	if st.Fields == nil {
		return
	}
	for _, field := range st.Fields.List {
		wireName, optional, stringFlag, skip, hasTag := e.parseJSONTag(field)
		if skip {
			continue
		}
		if pointerEmbedded {
			optional = true
		}

		if len(field.Names) == 0 {
			// Embedded (anonymous) field. Three paths:
			//  - json:"-"       — skipped above.
			//  - json:"name"    — emit as a single nested field under
			//                     that key at the current depth.
			//  - no tag / empty — recursively flatten the target
			//                     struct into the outer struct at
			//                     depth+1.
			if hasTag && wireName != "" {
				*out = append(*out, fieldInfo{
					jsonName: wireName,
					tsName:   formatFieldName(wireName),
					optional: optional,
					ti:       e.resolveFieldType(field, stringFlag),
					doc:      fieldDoc(field),
					depth:    depth,
					pos:      field.Pos(),
					tagged:   true,
				})
				continue
			}
			if stringFlag {
				e.panicAt(field.Pos(), "json ,string flag is not supported on embedded (flattened) fields")
			}
			e.flattenEmbedded(field.Type, depth, pointerEmbedded, out)
			continue
		}

		ti := e.resolveFieldType(field, stringFlag)
		for _, nameIdent := range field.Names {
			if !nameIdent.IsExported() {
				continue
			}
			name := wireName
			tagged := hasTag && wireName != ""
			if !tagged {
				name = nameIdent.Name
			}
			*out = append(*out, fieldInfo{
				jsonName: name,
				tsName:   formatFieldName(name),
				optional: optional,
				ti:       ti,
				doc:      fieldDoc(field),
				depth:    depth,
				pos:      nameIdent.Pos(),
				tagged:   tagged,
			})
		}
	}
}

// flattenEmbedded resolves an untagged embedded field's target type and
// recursively appends the target's fields into out at depth+1. Handles
// both value (`Base`) and pointer (`*Base`) embedding; pointer embedding
// forces every contributed field to be optional. Panics on the error
// conditions listed in §3.2 / the v0.2 feature plan: cross-package
// selector, generic instantiation, non-struct target, target with a
// MarshalJSON method, and embedding cycles.
func (e *emitter) flattenEmbedded(expr ast.Expr, depth int, pointerEmbedded bool, out *[]fieldInfo) {
	target := expr
	if star, ok := expr.(*ast.StarExpr); ok {
		pointerEmbedded = true
		target = star.X
	}
	switch t := target.(type) {
	case *ast.Ident:
		name := t.Name
		if e.hasMarshalJSON[name] {
			e.panicAt(t.Pos(),
				"embedded field %q declares a MarshalJSON method, which overrides the flattened wire shape. Tag it `json:\"name\"` to nest under that key, or register a TS shape with -map / //gents:map",
				name)
		}
		st, ok := e.allStructs[name]
		if !ok {
			if _, isAlias := e.namedAliases[name]; isAlias {
				e.panicAt(t.Pos(),
					"embedded field %q is not a struct type; only struct embedding can flatten. Tag it `json:\"name\"` to nest under that key, or embed the underlying struct directly",
					name)
			}
			e.panicAt(t.Pos(),
				"embedded field %q is not declared in the scanned input; cross-package flattening is not supported. Tag it `json:\"name\"` to nest and register %q via -map / //gents:map, or point -in at the directory containing the declaration",
				name, name)
		}
		if e.visiting[name] {
			e.panicAt(t.Pos(), "embedded-field cycle involving %q", name)
		}
		e.visiting[name] = true
		defer delete(e.visiting, name)
		e.collectFieldsDeep(st, depth+1, pointerEmbedded, out)
	case *ast.SelectorExpr:
		key := t.Sel.Name
		if x, ok := t.X.(*ast.Ident); ok {
			key = x.Name + "." + t.Sel.Name
		}
		e.panicAt(t.Pos(),
			"embedded field %q is declared in another package; cross-package flattening is not supported. Tag it `json:\"name\"` to nest and register %q via -map / //gents:map, or declare a local alias with the fields you need",
			key, key)
	case *ast.IndexExpr, *ast.IndexListExpr:
		e.panicAt(target.Pos(), "embedded field: generic-instantiation embedding (Box[T]) is not supported")
	default:
		e.panicAt(target.Pos(), "unsupported embedded field expression %T", target)
	}
}

// resolveDominantFields applies encoding/json's dominant-field rules
// (§3.2) to a flat list produced by collectFieldsDeep. Grouping is by
// jsonName; within each group we keep entries at the minimum depth, and
// when tagged and untagged entries co-exist at that depth we keep only
// the tagged ones (tag-presence disambiguates). Anything left over
// after both filters is a genuine ambiguity and panics with the source
// position of each surviving contribution. Emission order follows
// first-seen jsonName, so flattened Base fields appear at the embedded
// field's original position in the outer struct.
func (e *emitter) resolveDominantFields(all []fieldInfo, origName string) []fieldInfo {
	if len(all) == 0 {
		return all
	}
	byName := map[string][]fieldInfo{}
	order := make([]string, 0, len(all))
	for _, f := range all {
		if _, seen := byName[f.jsonName]; !seen {
			order = append(order, f.jsonName)
		}
		byName[f.jsonName] = append(byName[f.jsonName], f)
	}
	out := make([]fieldInfo, 0, len(order))
	for _, name := range order {
		group := byName[name]
		winner, ok := pickDominant(group)
		if ok {
			out = append(out, winner)
			continue
		}
		locs := make([]string, 0, len(group))
		minDepth := group[0].depth
		for _, f := range group {
			if f.depth < minDepth {
				minDepth = f.depth
			}
		}
		var ambiguous []fieldInfo
		for _, f := range group {
			if f.depth == minDepth {
				ambiguous = append(ambiguous, f)
				locs = append(locs, e.fset.Position(f.pos).String())
			}
		}
		e.panicAt(ambiguous[0].pos,
			"ambiguous JSON field %q in struct %q: %d contributions at depth %d (%s). Disambiguate with explicit json tags or by moving one field to a different level",
			name, origName, len(ambiguous), minDepth, strings.Join(locs, ", "))
	}
	return out
}

// pickDominant returns the single winning field for a group sharing one
// jsonName, or ok=false if the group is ambiguous. Rule order matches
// encoding/json's: least-nested wins; within the minimum depth, tagged
// wins over untagged; anything else is ambiguous.
func pickDominant(group []fieldInfo) (fieldInfo, bool) {
	if len(group) == 1 {
		return group[0], true
	}
	minDepth := group[0].depth
	for _, f := range group[1:] {
		if f.depth < minDepth {
			minDepth = f.depth
		}
	}
	var atMin []fieldInfo
	for _, f := range group {
		if f.depth == minDepth {
			atMin = append(atMin, f)
		}
	}
	if len(atMin) == 1 {
		return atMin[0], true
	}
	var tagged, untagged []fieldInfo
	for _, f := range atMin {
		if f.tagged {
			tagged = append(tagged, f)
		} else {
			untagged = append(untagged, f)
		}
	}
	if len(tagged) == 1 && len(untagged) > 0 {
		return tagged[0], true
	}
	return fieldInfo{}, false
}

// ---------------------------------------------------------------------------
// Type mapping

func (e *emitter) mapGoType(expr ast.Expr) typeInfo {
	switch t := expr.(type) {
	case *ast.Ident:
		return e.mapIdent(t)
	case *ast.SelectorExpr:
		return e.mapSelector(t)
	case *ast.StarExpr:
		return e.mapStar(t)
	case *ast.ArrayType:
		return e.mapArray(t)
	case *ast.MapType:
		return e.mapMap(t)
	case *ast.InterfaceType:
		if t.Methods == nil || len(t.Methods.List) == 0 {
			return typeInfo{ts: "unknown", zero: "null"}
		}
		e.panicAt(t.Pos(), "interface types with methods are not supported (only empty interface / any)")
	case *ast.StructType:
		e.panicAt(t.Pos(), "inline anonymous struct types are not supported as field types")
	case *ast.ChanType:
		e.panicAt(t.Pos(), "channel types cannot be marshaled to JSON")
	case *ast.FuncType:
		e.panicAt(t.Pos(), "function types cannot be marshaled to JSON")
	}
	e.panicAt(expr.Pos(), "unsupported Go type expression %T", expr)
	return typeInfo{}
}

func (e *emitter) mapIdent(t *ast.Ident) typeInfo {
	if ti, ok := e.resolveTypeMap(t.Name, t.Pos()); ok {
		return ti
	}
	switch t.Name {
	case "string":
		return typeInfo{"string", "''"}
	case "bool":
		return typeInfo{"boolean", "false"}
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64", "byte", "rune":
		return typeInfo{"number", "0"}
	case "any":
		return typeInfo{"unknown", "null"}
	}
	if factory, ok := e.marked[t.Name]; ok {
		return typeInfo{t.Name, "new" + factory + "()"}
	}
	// Auto-resolve in-file named aliases (e.g. `type UserID string`).
	// Safe for named primitives without custom MarshalJSON; panics
	// loudly for types that DO have MarshalJSON, because the wire
	// shape differs from the underlying type.
	if rhs, ok := e.namedAliases[t.Name]; ok {
		if e.hasMarshalJSON[t.Name] {
			e.panicAt(t.Pos(), "type %q declares a MarshalJSON method; its JSON wire shape differs from its underlying type, so auto-resolution would be wrong. Supply an explicit -map %s=<tsType> instead", t.Name, t.Name)
		}
		if e.resolving[t.Name] {
			e.panicAt(t.Pos(), "cycle in type-alias resolution involving %q — Go would reject this at compile time, but gents parses without type-checking and hits the cycle recursively", t.Name)
		}
		e.resolving[t.Name] = true
		defer delete(e.resolving, t.Name)
		return e.mapGoType(rhs)
	}
	e.panicAt(t.Pos(), "unsupported named type %q: expected a primitive, any/interface{}, a sibling struct marked with //gents:export in the same input, or a type declared via -map. If %q lives in another file, point -in at the directory instead (library: GenerateDir)", t.Name, t.Name)
	return typeInfo{}
}

func (e *emitter) mapSelector(t *ast.SelectorExpr) typeInfo {
	pkg, ok := t.X.(*ast.Ident)
	if !ok {
		e.panicAt(t.Pos(), "unsupported qualified type expression")
	}
	key := pkg.Name + "." + t.Sel.Name
	if ti, ok := e.resolveTypeMap(key, t.Pos()); ok {
		return ti
	}
	switch key {
	case "time.Time":
		// `time.Time{}` serialises to `"0001-01-01T00:00:00Z"`, but a
		// factory zero is consumed by the SPA before any wire round-trip
		// — it scaffolds an empty form, not a real timestamp. Empty
		// string is the falsy/"no value yet" sentinel JS code already
		// uses for unset string fields, and it matches the rest of the
		// tool's "string TS type → '' zero" rule from §2.3 of the
		// design doc. Round-tripping a Go `time.Time{}` through the
		// wire is rare and never desired in a fresh-form context.
		return typeInfo{"string", "''"}
	case "time.Duration":
		return typeInfo{"number", "0"}
	case "json.RawMessage":
		return typeInfo{"unknown", "null"}
	}
	e.panicAt(t.Pos(), "unsupported qualified type %s: add it via -map (e.g. -map %s=string)", key, key)
	return typeInfo{}
}

func (e *emitter) mapStar(t *ast.StarExpr) typeInfo {
	if _, isStar := t.X.(*ast.StarExpr); isStar {
		e.panicAt(t.Pos(), "double pointers (**T) are not supported; use *T with json:\",omitempty\"")
	}
	inner := e.mapGoType(t.X)
	return typeInfo{ts: inner.ts + " | null", zero: "null"}
}

func (e *emitter) mapArray(t *ast.ArrayType) typeInfo {
	if t.Len != nil {
		e.panicAt(t.Pos(), "fixed-length Go arrays are not supported; use a slice instead")
	}
	// encoding/json special-cases []byte (and the alias []uint8) as base64 strings.
	if ident, ok := t.Elt.(*ast.Ident); ok {
		if ident.Name == "byte" || ident.Name == "uint8" {
			return typeInfo{"string", "''"}
		}
	}
	inner := e.mapGoType(t.Elt)
	return typeInfo{wrapIfUnion(inner.ts) + "[]", "[]"}
}

func (e *emitter) mapMap(t *ast.MapType) typeInfo {
	keyIdent, ok := t.Key.(*ast.Ident)
	if !ok || keyIdent.Name != "string" {
		e.panicAt(t.Pos(), "only string-keyed maps are supported (got key type %T)", t.Key)
	}
	val := e.mapGoType(t.Value)
	return typeInfo{"Record<string, " + val.ts + ">", "{}"}
}

// ---------------------------------------------------------------------------
// Emission

func (e *emitter) emit(blocks []constBlock, structs []structInfo) string {
	var sb strings.Builder
	sb.WriteString("// Code generated by github.com/mmalcek/gents; DO NOT EDIT.\n")
	for _, b := range blocks {
		sb.WriteString("\n")
		e.emitConstBlock(&sb, b)
	}
	for _, s := range structs {
		sb.WriteString("\n")
		e.emitInterface(&sb, s)
		sb.WriteString("\n")
		e.emitFactory(&sb, s)
	}
	return sb.String()
}

// emitJSDoc writes a JSDoc block at the given indent. lines may be
// empty; src, when non-empty, is appended as a final `(source: file.go)`
// line. Nothing is written when both are empty. A lone line collapses to
// the single-line /** ... */ form.
func emitJSDoc(sb *strings.Builder, indent string, lines []string, src string) {
	all := make([]string, 0, len(lines)+1)
	for _, l := range lines {
		// A literal */ inside a doc comment would terminate the JSDoc
		// block early; escape it.
		all = append(all, strings.ReplaceAll(l, "*/", `*\/`))
	}
	if src != "" {
		all = append(all, "(source: "+src+")")
	}
	if len(all) == 0 {
		return
	}
	if len(all) == 1 {
		sb.WriteString(indent + "/** " + all[0] + " */\n")
		return
	}
	sb.WriteString(indent + "/**\n")
	for _, l := range all {
		sb.WriteString(indent + " *")
		if l != "" {
			sb.WriteString(" " + l)
		}
		sb.WriteString("\n")
	}
	sb.WriteString(indent + " */\n")
}

func (e *emitter) emitConstBlock(sb *strings.Builder, b constBlock) {
	emitJSDoc(sb, "", b.doc, b.src)
	for _, c := range b.consts {
		emitJSDoc(sb, "", c.doc, "")
		sb.WriteString("export const " + c.name + " = " + c.value + "\n")
	}
}

func (e *emitter) emitInterface(sb *strings.Builder, s structInfo) {
	emitJSDoc(sb, "", s.doc, s.src)
	sb.WriteString("export interface ")
	sb.WriteString(s.origName)
	if len(s.fields) == 0 {
		sb.WriteString(" {}\n")
		return
	}
	sb.WriteString(" {\n")
	for _, f := range s.fields {
		emitJSDoc(sb, "  ", f.doc, "")
		sb.WriteString("  ")
		sb.WriteString(f.tsName)
		if f.optional {
			sb.WriteString("?")
		}
		sb.WriteString(": ")
		sb.WriteString(f.ti.ts)
		sb.WriteString("\n")
	}
	sb.WriteString("}\n")
}

// ---------------------------------------------------------------------------
// Const expression rendering

// tsBinaryOps maps the Go binary operators whose TS spelling and
// semantics match closely enough to emit. Deliberately absent: / and %
// (Go const division is exact/integer, JS division is float — silent
// value drift), and &^ (no TS equivalent). Note that JS bitwise ops
// operate on 32-bit integers; bit-flag constants are fine, giant shifted
// masks are not.
var tsBinaryOps = map[token.Token]string{
	token.ADD: "+",
	token.SUB: "-",
	token.MUL: "*",
	token.AND: "&",
	token.OR:  "|",
	token.XOR: "^",
	token.SHL: "<<",
	token.SHR: ">>",
}

// renderConstExpr renders a Go const value expression as TS source.
// Supported: int/float/string literals, true/false, references to
// exported consts declared earlier in the input, unary -/^ (^ becomes
// ~), parens, and the operators in tsBinaryOps. Everything else panics —
// including iota, because gents doesn't evaluate constants and TS has no
// equivalent; the fix is always "write explicit values".
func (e *emitter) renderConstExpr(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.BasicLit:
		switch t.Kind {
		case token.INT:
			return renderIntLit(t.Value)
		case token.FLOAT:
			return t.Value
		case token.STRING:
			s, err := strconv.Unquote(t.Value)
			if err != nil {
				e.panicAt(t.Pos(), "cannot parse string literal %s: %v", t.Value, err)
			}
			return tsQuote(s)
		}
		e.panicAt(t.Pos(), "unsupported const literal %s (rune and imaginary literals are not supported)", t.Value)
	case *ast.Ident:
		switch t.Name {
		case "true", "false":
			return t.Name
		case "iota":
			e.panicAt(t.Pos(), "iota is not supported in exported consts; write explicit values")
		}
		if _, ok := e.constNames[t.Name]; ok {
			return t.Name
		}
		e.panicAt(t.Pos(), "const expression references %q, which is not an exported const declared earlier in the input (TS const declarations cannot forward-reference)", t.Name)
	case *ast.BinaryExpr:
		op, ok := tsBinaryOps[t.Op]
		if !ok {
			e.panicAt(t.OpPos, "unsupported operator %q in exported const expression (division and %%/&^ have different semantics in TS — precompute the value)", t.Op.String())
		}
		return e.renderConstOperand(t.X, t.Op) + " " + op + " " + e.renderConstOperand(t.Y, t.Op)
	case *ast.ParenExpr:
		return "(" + e.renderConstExpr(t.X) + ")"
	case *ast.UnaryExpr:
		switch t.Op {
		case token.SUB:
			return "-" + e.renderConstExpr(t.X)
		case token.XOR:
			return "~" + e.renderConstExpr(t.X)
		}
		e.panicAt(t.Pos(), "unsupported unary operator %q in exported const expression", t.Op.String())
	}
	e.panicAt(expr.Pos(), "unsupported const expression %T (supported: literals, earlier exported const references, and +, -, *, &, |, ^, <<, >> expressions)", expr)
	return ""
}

// renderConstOperand parenthesizes nested binary sub-expressions unless
// they repeat the parent operator (left-associative chains like a|b|c|d
// stay flat). Go and TS precedence tables differ (& binds tighter than +
// in Go, looser in TS), so explicit parens are the only safe way to
// preserve Go's grouping.
func (e *emitter) renderConstOperand(expr ast.Expr, parentOp token.Token) string {
	if b, ok := expr.(*ast.BinaryExpr); ok && b.Op != parentOp {
		return "(" + e.renderConstExpr(expr) + ")"
	}
	return e.renderConstExpr(expr)
}

// renderIntLit emits a Go integer literal as valid TS. Almost everything
// passes through verbatim (hex, binary, 0o octal, _ separators); the one
// exception is Go's legacy 0-prefixed octal (017), which is a syntax
// error in TS modules and is re-emitted as decimal.
func renderIntLit(v string) string {
	if len(v) > 1 && v[0] == '0' && !strings.ContainsAny(v[1:2], "xXbBoO_") {
		if n, err := strconv.ParseInt(v, 0, 64); err == nil {
			return strconv.FormatInt(n, 10)
		}
	}
	return v
}

// tsQuote renders s as a single-quoted TS string literal — matching the
// quote style factories already use for string zeros.
func tsQuote(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\'':
			b.WriteString(`\'`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String()
}

func (e *emitter) emitFactory(sb *strings.Builder, s structInfo) {
	sb.WriteString("export function new")
	sb.WriteString(s.factoryBase)
	sb.WriteString("(): ")
	sb.WriteString(s.origName)
	sb.WriteString(" {\n")

	var required []fieldInfo
	for _, f := range s.fields {
		if !f.optional {
			required = append(required, f)
		}
	}
	if len(required) == 0 {
		sb.WriteString("  return {}\n}\n")
		return
	}
	sb.WriteString("  return {\n")
	for _, f := range required {
		sb.WriteString("    ")
		sb.WriteString(f.tsName)
		sb.WriteString(": ")
		sb.WriteString(f.ti.zero)
		sb.WriteString(",\n")
	}
	sb.WriteString("  }\n}\n")
}
