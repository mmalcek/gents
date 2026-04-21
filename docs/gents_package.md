# gents — reference documentation

> **Module:** github.com/mmalcek/gents
> **License:** MIT
> This document is the authoritative specification of gents's behavior,
> API, and limits. If the code and this document disagree, the code is
> definitive — open an issue so we can fix the doc.

---

## 1. Overview

gents is a Go→TypeScript types generator. It reads Go source files, finds
structs marked with `//gents:export`, and emits matching TypeScript
interfaces plus zero-value factory functions.

**Guiding rule:** gents emits the TS shape that matches what
`encoding/json.Marshal` produces on the wire. This defines every type
mapping, zero value, and tag-handling decision.

**What it's for:** keeping Go struct shapes and TypeScript types synchronized
at build time, so a Go server and a TypeScript client can never drift out
of sync on wire shape.

**What makes it different from [tygo](https://github.com/gzuidhof/tygo):**

- **Factory functions alongside interfaces.** `newItem()` returns a value
  that round-trips through Go's `encoding/json` cleanly — including the
  wire-accurate zeros for `time.Time` and the base64 encoding for
  `[]byte`.
- **Marker-based opt-in.** Only structs annotated with `//gents:export`
  are processed. No whole-package scan driven by a config file.
- **Pure stdlib.** No third-party dependencies.

Projects that need tygo's broader feature set (config-driven type overrides,
JSDoc emission from Go doc comments, etc.) should use tygo. Projects that
want factory emission, marker opt-in, and a minimal dependency footprint
land here.

---

## 2. Design principles

Listed in priority order. When they pull against each other, the higher
one wins.

1. **Simple beats generic.** Pick the smaller API surface. 90% of use
   cases with no config beats 100% with twenty flags.
2. **One job.** gents turns a marked Go struct into a TS interface +
   factory. Anything else (request/response splitting, validation,
   OpenAPI import) belongs in a different tool or at the consumer's Go
   layer.
3. **`json:` is the only tag gents reads.** No other struct tag exists
   from gents's point of view. GORM, `binding`, `validate`, `msgpack`,
   `xml`, `yaml`, `bson`, anything custom — all invisible. gents's
   contract is "emit the TS shape that matches what `encoding/json`
   would produce." That contract is well-defined; coupling to any other
   tag would bind gents to a specific consumer's stack.
4. **Explicit beats implicit.** Marker comments are opt-in per struct.
   No whole-package scan. No auto-detection. If a behavior would
   surprise a reader, don't do it.
5. **Pure stdlib.** `go/ast`, `go/parser`, `reflect.StructTag`, and the
   usual. No third-party dependencies.
6. **Deterministic output.** Two runs on the same input produce
   byte-identical output. Source order preserved, consistent
   indentation, exactly one trailing newline.
7. **Fail loud on the impossible; match Go on the optional.**
   Unsupported Go types panic with a `file:line` pointer — the emitter
   genuinely cannot produce output for them. Where Go's stdlib has a
   sensible default (e.g. `encoding/json` using the Go field name when
   no `json:` tag is present), gents adopts the same default rather
   than inventing a stricter rule.

---

## 3. Specification

### 3.1 Marker

A struct opts in via a doc comment containing this exact line:

```go
//gents:export
type Foo struct { ... }
```

Detection is strict:

- The marker must be a `//`-style line comment.
- After stripping the leading `//`, the content must be exactly
  `gents:export` — no leading/trailing whitespace, nothing after.
- Block comments (`/* gents:export */`) never match.
- The marker may appear in the doc comment group attached to the `type`
  declaration (`GenDecl.Doc`) or to an individual `TypeSpec` inside a
  grouped declaration (`TypeSpec.Doc`). A group-level marker applies to
  every type spec in the group.

Unmarked structs are invisible to gents. A file containing only
unmarked structs produces an empty output and the CLI writes no file.

**Marker on a non-struct type (type alias, interface, named primitive,
generic instantiation) panics** with a clear message. Silent-skip on
an explicit marker would violate principle 7 ("fail loud") — the user
asked for emission; if gents can't emit, it says so with `file:line`.
Workaround: drop the marker, or emit a wrapping struct.

### 3.2 Field emission

For every exported field on a marked struct:

- `json:"-"` → field is skipped entirely (matches `encoding/json`).
- `json:"name"` → field emitted as `name: <tsType>`.
- `json:"name,omitempty"` or `json:"name,omitzero"` → field emitted as
  `name?: <tsType>`; the factory omits it from the zero-value object.
- `json:",omitempty"` (empty name, flag present) → Go field name used;
  field marked optional. Matches `encoding/json` semantics.
- `json:""` (empty tag) → treated as if no tag were present.

For **embedded (anonymous) fields**:

- `Base `json:"-"`` → silently skips the embedded field (same as any
  other `json:"-"`).
- `Base `json:"name"`` → emits the embedded type as a nested object
  under that key. Equivalent to writing `Name Base `json:"name"`` and
  resolves the same way (the embedded type must be recognized — a
  marked sibling, a primitive, or registered via `-map`).
- `Base` with no tag → **flattened** (v0.2+). Base's exported fields
  are promoted onto the outer struct at depth 1, matching
  `encoding/json.Marshal`'s wire shape. Chains (`Foo` embeds `Bar`
  embeds `Baz`) flatten recursively. `*Base` flattens the same way
  but marks every contributed field optional, mirroring the "nil
  pointer omits the embedded fields" behavior. Dominant-field rules
  apply on collision: least-nested wins; within the minimum depth,
  tagged wins over untagged; anything else panics as ambiguous with
  the source location of each surviving contribution. See §7.1 for
  what the v0.2 implementation can and cannot resolve.
- **No `json` tag on an exported field** → field emitted using the Go
  field name verbatim, matching `encoding/json`'s own default.
- Unexported fields (lowercase Go names) are invisible, same as
  `encoding/json` ignores them.

Struct tags other than `json:` (gorm, binding, validate, xml, msgpack,
custom) coexist peacefully and are ignored.

### 3.3 Type mapping

The full mapping, reflecting `encoding/json.Marshal` wire behavior:

| Go type | TypeScript | Factory zero |
|---|---|---|
| `string` | `string` | `''` |
| `bool` | `boolean` | `false` |
| `int`, `int8..int64`, `uint`, `uint8..uint64`, `float32`, `float64`, `byte`, `rune` | `number` | `0` |
| `time.Time` | `string` (RFC3339) | `'0001-01-01T00:00:00Z'` |
| `*time.Time` | `string \| null` | `null` |
| `time.Duration` | `number` (nanoseconds, int64-backed) | `0` |
| `json.RawMessage` | `unknown` | `null` |
| `any` / `interface{}` (empty method set) | `unknown` | `null` |
| `[]byte` / `[]uint8` | `string` (base64 — `encoding/json` special case) | `''` |
| `*[]byte` | `string \| null` | `null` |
| `[]T` (T ≠ byte/uint8) | `T[]` | `[]` |
| `map[string]V` | `Record<string, V>` (only string-keyed maps supported) | `{}` |
| `*T` (other) | `T \| null` | `null` |
| Sibling struct marked with `//gents:export` | stripped interface name (see §3.5) | `new<StrippedName>()` |
| Any Go type registered via `-map` / `Options.TypeMap` (§3.9) | as mapped | inferred from the TS expression |

Unsupported types panic with a `file:line` pointer and an actionable
message:

- Embedded (anonymous) fields with no `json:` tag → **flattened**
  (v0.2+). Target struct must live in the scanned input; cross-package
  embedding panics with a pointer at the `-map` / `//gents:map`
  workaround. Ambiguous field contributions (two equally-qualified
  entries at the same depth) panic with both locations. See §7.1.
- `[N]T` fixed-length Go arrays → use a slice.
- `**T` double pointer → use a single pointer with `omitempty`.
- Inline anonymous `struct{...}` as a field type.
- Non-stdlib qualified types (`uuid.UUID`, `decimal.Decimal`,
  `pkg.Whatever`) → register via `-map`.
- Named primitive aliases (`type MyString string`) → register via `-map`.
- Interfaces with non-empty method sets.
- `chan`, `func`, `complex64`, `complex128`.
- Non-string-keyed maps.

### 3.4 JSON tag modifiers

Full list of modifiers gents recognizes inside the `json:"..."` tag
(after the name part):

| Modifier | Effect |
|---|---|
| `omitempty` | Field marked optional in TS (`?:`), factory omits it. |
| `omitzero` (Go 1.24+) | Identical to `omitempty` for TS emission. The Go-side difference (uses `reflect.IsZero` / a user-defined `IsZero()` method, which handles `time.Time` correctly) doesn't change the wire contract or the TS shape. |
| `string` | Coerces the field's TS type to reflect `encoding/json`'s `,string` wire wrapping. See below. |

Any other flag → panic with `file:line`.

#### The `,string` tag modifier

When a field is tagged `json:"name,string"`, `encoding/json` wraps the
encoded value in a JSON string on the wire. gents coerces the TS type to
match:

| Base (before `,string`) | With `,string` → TS type | Factory zero |
|---|---|---|
| `number` (any numeric type, `time.Duration`) | `string` | `'0'` |
| `boolean` | `string` | `'false'` |
| `number \| null` (pointer to numeric) | `string \| null` | `null` |
| `boolean \| null` (pointer to bool) | `string \| null` | `null` |
| `string` / `string \| null` (string, `*string`, `[]byte`, `time.Time`, etc.) | **panic** — base is already string; flag would double-encode | — |
| Anything else (slice, map, struct, interface, `json.RawMessage`) | **panic** — `encoding/json` itself silently ignores `,string` on these types; gents rejects loud | — |

Example:

```go
type User struct {
    ID int64 `json:"id,string"`
}
```

emits `id: string` with factory zero `'0'`. The on-wire shape is
`{"id":"12345"}`, which matches the TS `string` type. Without `,string`
the wire shape would be `{"id":12345}` and the TS type would be `number`.

Common use: `int64` / `uint64` IDs sent to JavaScript clients (JS's
`Number` can't represent int64 precisely past 2^53, so stringifying on
the wire is the standard mitigation).

### 3.5 Naming convention

Given a Go struct name `S` and a strip prefix `P` (CLI flag `-strip`,
default `""` meaning no stripping):

- Let `N = StripPrefix(S, P)` (if `P` is non-empty and `S` starts with
  `P`, drop it; else `N = S`).
- Interface name = `N`.
- Factory name = `new` + `N`.
- Cross-struct references also emit as `N` — the stripped name is the
  canonical TS name everywhere.

| Go struct | `-strip` | TS interface | TS factory |
|---|---|---|---|
| `Foo` | `""` (default) | `Foo` | `newFoo` |
| `tFoo` | `""` (default) | `tFoo` | `newtFoo` |
| `tFoo` | `"t"` | `Foo` | `newFoo` |
| `cFoo` | `"c"` | `Foo` | `newFoo` |

Default is **no stripping** because gents takes no position on Go naming
convention. Projects that use a prefix convention opt in explicitly.

### 3.6 Non-identifier JSON names

JSON field names are arbitrary strings. TS interface property names and
object literal keys can be bare identifiers OR string literals. If the
JSON name doesn't match `^[A-Za-z_$][A-Za-z0-9_$]*$`, gents quotes it
with single quotes:

```go
type Header struct {
    ContentType string `json:"content-type"`
    StatusCode  int    `json:"123status"`
}
```

emits

```ts
export interface Header {
  'content-type': string
  '123status': number
}

export function newHeader(): Header {
  return {
    'content-type': '',
    '123status': 0,
  }
}
```

Applied identically in the interface and the factory body.

### 3.7 Cross-struct references

Within a single input (file or bundle), one marked struct may reference
another by name:

```go
//gents:export
type Outer struct {
    Inner Inner `json:"inner"`
}

//gents:export
type Inner struct {
    Value string `json:"value"`
}
```

The emitter resolves the reference at Pass 2 by looking up the target's
stripped TS name (§3.5), and uses the corresponding `new<StrippedName>()`
call as the factory zero. Source declaration order doesn't matter — a
two-pass walk collects marked names first, then emits fields.

A reference to an **unmarked** struct panics with `file:line`. Silently
emitting `any` would hide a missing marker.

In bundle mode (§3.8), cross-file references resolve the same way,
because all marked structs in the directory tree share one unified
name map.

### 3.8 Bundle mode

Bundle mode kicks in automatically when `-in` points at a directory (or
when `-in` is omitted — the CLI defaults to the current directory).
Programmatically, `GenerateDir(dirPath, opts)` is the direct entry point.

Bundle mode walks the directory recursively, collecting every `.go` file
and skipping:

- `_test.go` files (Go test conventions)
- `testdata/` directories (Go toolchain convention)
- `_`-prefixed and `.`-prefixed files and directories

It emits a single TS file covering every marked struct across the tree.

Ordering is deterministic:

- Files sorted alphabetically by path.
- Structs within each file emitted in source order.
- Fields within each struct emitted in source order.

Pass 1 builds one unified marked-struct map across all files. Pass 2
emits fields using that map, so cross-file references resolve
naturally.

Collisions are detected at Pass 1:

- Two marked structs sharing the same Go name (possibly in different
  files / packages): **panic** with both source locations.
- Two marked structs mapping to the same TS name after stripping:
  **panic** with both source locations.

### 3.9 In-file named-alias auto-resolution

Named primitive aliases defined in the scanned input resolve automatically,
no flag required:

```go
type UserID string
type Score int

//gents:export
type User struct {
    ID    UserID `json:"id"`     // emits as string
    Score Score  `json:"score"`  // emits as number
}
```

How it works:

1. During Pass 1, gents records every top-level non-struct type
   declaration (`type X ...`) in an alias map.
2. During Pass 1, it also records which types declare a `MarshalJSON`
   method in the scanned input.
3. When `mapIdent` hits an unknown identifier, it consults the alias
   map. If found AND no `MarshalJSON` is declared, it recursively maps
   the RHS expression — yielding the primitive type (or nested alias)
   at the end of the chain.
4. If the type DOES declare a `MarshalJSON` method, auto-resolution
   would miss the custom wire shape. gents panics with a hint to use
   `-map` (or `Options.TypeMap`) to declare the real wire type.
5. Cycle detection: `type A B; type B A` panics instead of looping.

Chain resolution works: `type A B; type B C; type C string` resolves
`A` to `string`.

**This does not cross package boundaries.** `uuid.UUID` still needs
`-map` — its definition lives in an external package gents doesn't
load. Even if gents could find the source, `uuid.UUID` has a
`MarshalJSON` method that emits a string despite its `[16]byte`
underlying type. Auto-resolution based on underlying types would
produce wrong output for any third-party type with custom marshaling
(most interesting ones). `-map` is the only correct mechanism for
cross-package types.

**Opt-out:** supply an explicit mapping via `-map` or `Options.TypeMap`
(which takes precedence over auto-resolution) to override any resolved
type.

### 3.10 Custom type mappings

For Go types gents doesn't recognize out of the box — third-party types,
arbitrary qualified selectors, or named aliases that declare
`MarshalJSON` — the user supplies mappings via one of three equivalent
mechanisms:

1. **CLI flag**: `-map GoType=TSType` (repeatable).
2. **Source directive**: `//gents:map GoType=TSType` written anywhere in
   the scanned input. Global by default — a directive in one file
   applies to references from every other file in the bundle. Typically
   placed alongside a `//go:generate` line at the top of the file that
   knows which types it depends on.
3. **Library field**: `Options.TypeMap map[string]string`.

Precedence: CLI flag > source directive > built-in defaults. CLI
overrides silently (explicit runtime choice wins). Two directives that
disagree about the same Go type in the same bundle → panic with both
locations.

(In-file named aliases without custom marshaling are handled
automatically — see §3.9.)

Key form:

- Unqualified: `MyString` — matches `*ast.Ident.Name`.
- Qualified: `pkg.Name` — matches `pkg.Name` in an `*ast.SelectorExpr`.

User mappings take **precedence over built-ins**, so
`-map time.Time=Date|null` overrides the default.

Factory zeros are inferred from the TS type:

| TS type shape | Inferred zero |
|---|---|
| `string` | `''` |
| `number` | `0` |
| `boolean` | `false` |
| `unknown` | `null` |
| `X \| null` / `null \| X` (contains `null` in a union) | `null` |
| `X[]` | `[]` |
| `Record<...>` | `{}` |
| anything else (named types like `Date`, arbitrary unions like `Active \| Pending`) | **panic** at emit time — add `\| null` to the mapping to make it nullable, or use library `Options.TypeMap` with a pre-computed value |

Pointer/slice/map wrapping applies by recursion: with `-map
uuid.UUID=string` in place, `*uuid.UUID` → `string | null`,
`[]uuid.UUID` → `string[]`, `map[string]uuid.UUID` → `Record<string,
string>`. No extra configuration needed.

**Collision with generated interfaces:** if a mapped TS name matches a
generated interface name (e.g. `-map Something=User` combined with
`//gents:export type User struct{}`), gents panics at Pass 1 with both
source locations. Same rule as the strip-induced collision check in
§3.8.

**Example — co-locating mappings with `//go:generate`:**

```go
package api

//go:generate go run github.com/mmalcek/gents/cmd/gents@v0.1.0 -out ../client/types.ts

//gents:map uuid.UUID=string
//gents:map decimal.Decimal=string
//gents:map time.Time=Date | null

import (
    "github.com/google/uuid"
    "github.com/shopspring/decimal"
    "time"
)

//gents:export
type User struct {
    ID     uuid.UUID       `json:"id"`
    Amount decimal.Decimal `json:"amount"`
    Joined time.Time       `json:"joined"`
}
```

The three `//gents:map` lines live with the source that references the
types; the `go:generate` line stays clean. Any other file in the bundle
that uses `uuid.UUID` etc. benefits from the same mappings without
repeating them.

### 3.11 Output format invariants

All output obeys these invariants (verified by `TestIdempotent`):

- Header: exactly `// Code generated by github.com/mmalcek/gents; DO
  NOT EDIT.\n`
- One blank line before the first struct.
- Interface: `export interface <Name> {` ... `}\n`, fields one per
  line, 2-space indent.
- One blank line between the interface and its factory.
- Factory: `export function new<Name>(): <Name> {\n  return {\n ...\n
  }\n}\n`, fields one per line, 4-space indent for object literal.
- Empty struct: `export interface <Name> {}\n` on a single line.
- Factory for all-optional struct: `return {}\n`.
- One blank line between structs.
- Exactly one trailing newline at EOF.

---

## 4. CLI reference

```
gents [-in <path>] -out <output.ts> [flags]

  -in string      Go source file or directory. Directories are walked
                  recursively (bundle mode). Defaults to the current
                  directory when omitted.
  -out string     TypeScript file to write (required).
  -strip string   Prefix to strip from Go struct names. Applied uniformly
                  to interface names, factory names, and cross-struct
                  references. Empty = verbatim (default).
  -force          Overwrite the output file even if it wasn't generated
                  by gents (default: refuse).
  -map KEY=VAL    Custom Go-to-TS type mapping (§3.9). Repeatable.
```

Invocation patterns:

```
gents -out types.ts                    # bundle the current directory
gents -in api/ -out api.ts             # bundle a subtree
gents -in item.go -out item.ts         # single file
```

Only `-out` is strictly required. gents stat's the `-in` path to decide:
file → single-file emission; directory → bundle-mode emission.

### Overwrite guardrail

Before writing `-out`, the CLI checks whether an existing file at that
path begins with the gents-generated header (`// Code generated by
github.com/mmalcek/gents; DO NOT EDIT.`). If the first line doesn't
match, the write is refused with a clear error. Protects against
foot-gunning a hand-written `.ts` file whose path happens to collide
with an output path. Pass `-force` to bypass.

### Parent directory creation

The CLI calls `os.MkdirAll` on the parent directory of `-out` before
writing. First-time use of `gents -in item.go -out ../client/types/item.ts`
works even when `types/` doesn't exist yet.

### Panic handling

Library-level panics (unsupported types, collisions, malformed tags)
are caught by the CLI's top-level `defer recover()` and printed as
single-line error messages with exit code 1. No stack trace in this
common path.

Runtime panics (nil deref, index out of range — i.e. bugs in gents
itself) are **re-panicked** so the user sees a full stack trace for
bug reports.

---

## 5. Library API

```go
package gents

// Options tunes emission. The zero value emits verbatim names.
type Options struct {
    // Strip is the prefix removed from Go struct names before
    // emitting them. Empty = no stripping.
    Strip string

    // TypeMap supplies user-defined Go-to-TS mappings. See §3.9.
    TypeMap map[string]string
}

// Generate parses a single Go source file and returns the TypeScript
// source for every //gents:export-marked struct. An empty string with
// a nil error means the input had no marked structs.
//
// Panics on unsupported Go types or malformed input. The panic payload
// is always an error; callers that need panics converted to errors
// should use defer+recover and type-assert.
func Generate(inPath string, opts Options) (string, error)

// GenerateDir is the bundle-mode entry point. Walks dirPath recursively,
// collects every .go file (skipping _test.go and _/. prefixed entries),
// and emits a single bundled TS source. Cross-file references resolve
// because all marked structs share one output.
func GenerateDir(dirPath string, opts Options) (string, error)
```

Two exported functions. One exported struct. Everything else is
internal.

See [`example_test.go`](../example_test.go) for runnable examples
rendered on pkg.go.dev.

---

## 6. Worked examples

### 6.1 Simple struct

```go
package example

import "time"

//gents:export
type Item struct {
    ID          string    `json:"id"`
    Name        string    `json:"name"`
    Description string    `json:"description,omitempty"`
    Tags        []string  `json:"tags"`
    CreatedAt   time.Time `json:"created_at"`
}
```

produces

```ts
// Code generated by github.com/mmalcek/gents; DO NOT EDIT.

export interface Item {
  id: string
  name: string
  description?: string
  tags: string[]
  created_at: string
}

export function newItem(): Item {
  return {
    id: '',
    name: '',
    tags: [],
    created_at: '0001-01-01T00:00:00Z',
  }
}
```

### 6.2 Cross-struct references

```go
//gents:export
type Outer struct {
    Inner Inner `json:"inner"`
}

//gents:export
type Inner struct {
    Value string `json:"value"`
}
```

```ts
export interface Outer {
  inner: Inner
}

export function newOuter(): Outer {
  return {
    inner: newInner(),
  }
}

export interface Inner {
  value: string
}

export function newInner(): Inner {
  return {
    value: '',
  }
}
```

### 6.3 Bundle mode

Given `api/user.go`:

```go
package api

//gents:export
type User struct {
    ID      string  `json:"id"`
    Profile Profile `json:"profile"`
}
```

and `api/profile.go`:

```go
package api

//gents:export
type Profile struct {
    Bio string `json:"bio"`
}
```

run `gents -in api/ -out types.ts`. The output bundles both
interfaces in one file and the `User.profile` reference resolves
correctly even though `Profile` is defined in a sibling file.

### 6.4 Custom type mappings

```go
package api

import (
    "github.com/google/uuid"
    "github.com/shopspring/decimal"
)

//gents:export
type Record struct {
    ID     uuid.UUID       `json:"id"`
    Maybe  *uuid.UUID      `json:"maybe"`
    Amount decimal.Decimal `json:"amount"`
}
```

run:

```
gents -in api.go -out api.ts \
  -map uuid.UUID=string \
  -map decimal.Decimal=string
```

produces:

```ts
export interface Record {
  id: string
  maybe: string | null
  amount: string
}

export function newRecord(): Record {
  return {
    id: '',
    maybe: null,
    amount: '',
  }
}
```

Pointer wrapping handled automatically by recursion — no need to map
`*uuid.UUID` separately.

### 6.5 Naming and stripping

Input struct `tFoo` with default `-strip=""` emits as `tFoo` / `newtFoo`:

```ts
export interface tFoo { ... }
export function newtFoo(): tFoo { ... }
```

With `-strip=t`:

```ts
export interface Foo { ... }
export function newFoo(): Foo { ... }
```

The stripped name is also what appears in cross-struct references from
other marked structs.

---

## 7. Limitations & non-goals

### 7.1 Current limitations

These panic today. Some may relax in future versions; none are design
mistakes.

- **Cross-package embedded flattening.** Default flattening works for
  embedded types declared inside the scanned input (single file or
  bundle directory) and walks chains recursively. What remains
  unsupported is an embedded type declared in *another* Go package
  (`gorm.Model`, `mongo.ID`, etc.) — gents doesn't load external
  packages, so it can't enumerate the fields. Workarounds: declare a
  local mirror with the fields you need, rewrite the field as
  `Base Base `json:"base"`` to nest instead of flatten, or register
  the type via `-map` / `//gents:map` for a TS placeholder. Also
  unsupported at any depth: embedding a type with a custom
  `MarshalJSON` method (the wire shape comes from the method, not
  field walking — gents panics with a pointer to the workaround) and
  generic-instantiation embedding (`Box[T]`).
- **Fixed-length Go arrays (`[N]T`).** `encoding/json` marshals these as
  JSON arrays, inconsistently with the `[]byte` base64 special case.
  Workaround: use a slice.
- **Double pointers (`**T`).** `encoding/json` semantics are confusing.
  Workaround: single pointer + `omitempty`.
- **Inline anonymous struct types as field types.** Would require
  synthesizing a TS name. Workaround: define the inner struct
  separately.
- **Non-string-keyed maps.** `encoding/json` requires keys to be
  strings (or text-marshaler-implementing). gents only supports literal
  `string`.
- **`,string` flag on non-numeric, non-boolean types.** `encoding/json`
  itself ignores it on slices, maps, structs, etc. gents panics loudly
  rather than producing wrong output.

### 7.2 Explicit non-goals

Out of scope indefinitely. Asks for these get closed with a pointer to
this section.

- **Request/Response split from a single Go struct.** gents emits one
  interface per marked struct. Whether a project uses single-struct or
  a Request/Response pair is an architectural choice gents doesn't
  take a position on. Projects that want a split mark two Go structs;
  projects that want unified mark one.
- **Generating Go → anything other than TypeScript.** gents is
  single-purpose.
- **Runtime reflection.** This is a build-time tool, always.
- **Importing OpenAPI / JSON Schema / protobuf definitions.** Different
  tool, different problem.
- **Generating TypeScript classes, decorators, Zod schemas, anything
  beyond `interface` + factory function.**
- **Validation / binding tag interpretation.** `binding:"required"`,
  `validate:"min=1"` — all invisible.
- **Watcher / dev-server mode.** External file-watching tools handle
  this fine; gents is a single pass.
- **Code formatting beyond determinism.** Output is predictable text,
  not prettier-compatible. Run your formatter of choice afterwards.
- **Non-`json:` tag serializers.** Only `encoding/json` semantics are
  honored. `msgpack`, `xml`, `bson`, etc. are ignored. A project using
  a non-`json` wire serializer shouldn't use gents.

---

## 8. Roadmap

v0.1.0 ships everything in §3. Remaining candidates for future releases:

- **JSDoc comment emission** from Go doc comments through to TS
  interface/property comments.
- **Stdout output** when `-out` is omitted, for shell pipelines.
- **Config-file alternative** to repeated `-map` flags (`-map-file
  gents.yaml`).
- **Library-side custom zero-value overrides** via a richer `TypeMap`
  shape (`map[string]TypeMapping` with explicit `TS` and `Zero` fields).
- **Mirror-mode output** (one TS file per Go file with `import`
  statements) — only if real projects ask; bundle mode already covers
  the functional case.
- **Cross-package embedded flattening.** Same-bundle flattening shipped
  in v0.2; resolving `gorm.Model` and friends would require loading
  external packages (`go/build` or `go/packages`) which pushes past
  the "tiny tool" ethos. Revisit only if real demand surfaces.

Nothing beyond this is planned until demand surfaces.

---

## 9. Versioning policy

- **v0.1.x** — patches fix bugs, never add features. Features go into
  v0.2+.
- **v0.x (x ≥ 1)** — new features; occasional breaking changes
  allowed. `CHANGELOG.md` documents every break.
- **v1.0** — API freeze. Breaking changes require v2. Promoted only
  after real-world use on two or more projects without surprises.

Breaking changes to watch for pre-v1:

- Changing the default value of `-strip` (currently `""`).
- Renaming the `//gents:export` marker.
- Adding a new required CLI flag.
- Changing the default emission format (e.g. `export type` instead of
  `export interface`).
- Evolving `Options.TypeMap` from `map[string]string` to a richer
  shape.

All of the above require a major version bump once v1 ships. Pre-v1,
document in the changelog and move on.
