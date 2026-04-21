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
// interface property and the factory object-literal key.
type fieldInfo struct {
	jsonName string
	tsName   string
	optional bool
	ti       typeInfo
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
	hasMarshalJSON map[string]bool              // types that declare a MarshalJSON method in the scanned input
	resolving      map[string]bool              // active alias-resolution set (cycle detection)
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

// collectAuxInfo records two things that power in-file type auto-resolution:
//
//  1. namedAliases — non-struct top-level type decls (e.g. `type UserID
//     string`). Stored as the RHS expression so mapIdent can recursively
//     map it later. Struct types are handled by collectMarked; anything
//     unmarked and non-struct goes here.
//
//  2. hasMarshalJSON — any type in the scanned input that declares a
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
				if _, isStruct := ts.Type.(*ast.StructType); isStruct {
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

func (e *emitter) collectFields(st *ast.StructType) []fieldInfo {
	var out []fieldInfo
	if st.Fields == nil {
		return out
	}
	for _, field := range st.Fields.List {
		// Parse the json tag up-front — applies the same whether the
		// field is named or embedded (json:"-" skips either kind;
		// json:"name" gives an embedded field an explicit nested name).
		wireName, optional, stringFlag, skip, hasTag := e.parseJSONTag(field)
		if skip {
			continue
		}

		if len(field.Names) == 0 {
			// Embedded (anonymous) field. Two paths are supported:
			//  - json:"-"       — already skipped above.
			//  - json:"name"    — nest the embedded type under that
			//                     key, matching encoding/json's
			//                     behavior for tagged embedded fields.
			// The third path — default flattening when no tag is
			// present — requires dominant-field resolution across
			// files/packages and is deferred to v0.2. Check this
			// before mapGoType so the user sees the flattening hint
			// instead of a confusing "unsupported named type" panic.
			if !hasTag || wireName == "" {
				e.panicAt(field.Pos(),
					"embedded (anonymous) field flattening is not yet supported. Workarounds: tag it `json:\"name\"` to nest the embedded type under that key, tag it `json:\"-\"` to skip it entirely, or rewrite it as an explicit named field.")
			}
			ti := e.mapGoType(field.Type)
			if stringFlag {
				ti = e.applyStringFlag(ti, field.Pos())
			}
			out = append(out, fieldInfo{
				jsonName: wireName,
				tsName:   formatFieldName(wireName),
				optional: optional,
				ti:       ti,
			})
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
			if !hasTag || name == "" {
				name = nameIdent.Name
			}
			out = append(out, fieldInfo{
				jsonName: name,
				tsName:   formatFieldName(name),
				optional: optional,
				ti:       ti,
			})
		}
	}
	return out
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
