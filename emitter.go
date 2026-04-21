package gents

import (
	"fmt"
	"go/ast"
	"go/token"
	"reflect"
	"regexp"
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
	depth    int       // 0 = directly on the outer struct; 1+ = contributed via embedding
	pos      token.Pos // source position of the contributing Go field (for diagnostics)
	tagged   bool      // true when jsonName came from a json:"..." tag with a non-empty name
}

type structInfo struct {
	origName string
	tsName   string
	fields   []fieldInfo
}

type emitter struct {
	fset           *token.FileSet
	marked         map[string]string    // original Go name -> stripped TS name
	origin         map[string]token.Pos // original Go name -> position of first definition (for collision diagnostics)
	strip          string
	typeMap        map[string]string            // final merged Go-to-TS mappings (directives + CLI overrides)
	directiveMap   map[string]directiveOriginPos // mappings collected from //gents:map directives across all scanned files
	namedAliases   map[string]ast.Expr          // in-file non-struct type decls for auto-resolution (e.g. `type UserID string`)
	allStructs     map[string]*ast.StructType   // every top-level struct declaration in the scanned input, marked or not (powers embedded flattening)
	hasMarshalJSON map[string]bool              // types that declare a MarshalJSON method in the scanned input
	resolving      map[string]bool              // active alias-resolution set (cycle detection)
	visiting       map[string]bool              // active embedded-flatten descent set (cycle detection)
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

var tsIdentRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

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

// panicAt aborts with a file:line-prefixed error wrapped in panic — the
// caller of Generate is expected to recover if they want errors.
func (e *emitter) panicAt(pos token.Pos, format string, args ...any) {
	position := e.fset.Position(pos)
	panic(fmt.Errorf("%s: %s", position, fmt.Sprintf(format, args...)))
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
// expression. Handles the cases listed in §2.3 "Custom type mappings" of
// the design doc. Returns an error (no panic) for unsupported shapes;
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
// the name of a struct gents is about to emit. Same invariant as §2.4's
// strip-induced collision check, extended to cover the -map flag.
func (e *emitter) checkTypeMapCollisions() {
	for goType, tsType := range e.typeMap {
		for origName, generatedTS := range e.marked {
			if tsType == generatedTS {
				e.panicAt(e.origin[origName],
					"mapped TS type %q (from -map %s=%s) collides with the generated interface for %q",
					tsType, goType, tsType, origName)
			}
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
				ti := e.mapGoType(field.Type)
				if stringFlag {
					ti = e.applyStringFlag(ti, field.Pos())
				}
				*out = append(*out, fieldInfo{
					jsonName: wireName,
					tsName:   formatFieldName(wireName),
					optional: optional,
					ti:       ti,
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

		ti := e.mapGoType(field.Type)
		if stringFlag {
			ti = e.applyStringFlag(ti, field.Pos())
		}
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
	if tsName, ok := e.marked[t.Name]; ok {
		return typeInfo{tsName, "new" + tsName + "()"}
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
		return typeInfo{"string", "'0001-01-01T00:00:00Z'"}
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
		e.panicAt(t.Pos(), "fixed-length Go arrays are not supported in v0.1; use a slice instead")
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

func (e *emitter) emit(structs []structInfo) string {
	var sb strings.Builder
	sb.WriteString("// Code generated by github.com/mmalcek/gents; DO NOT EDIT.\n")
	for _, s := range structs {
		sb.WriteString("\n")
		e.emitInterface(&sb, s)
		sb.WriteString("\n")
		e.emitFactory(&sb, s)
	}
	return sb.String()
}

func (e *emitter) emitInterface(sb *strings.Builder, s structInfo) {
	sb.WriteString("export interface ")
	sb.WriteString(s.tsName)
	if len(s.fields) == 0 {
		sb.WriteString(" {}\n")
		return
	}
	sb.WriteString(" {\n")
	for _, f := range s.fields {
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

func (e *emitter) emitFactory(sb *strings.Builder, s structInfo) {
	sb.WriteString("export function new")
	sb.WriteString(s.tsName)
	sb.WriteString("(): ")
	sb.WriteString(s.tsName)
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
