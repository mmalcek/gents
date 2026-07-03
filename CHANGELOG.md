# Changelog

All notable changes to gents are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Const-block export.** `//gents:export` on a `const` declaration
  emits each spec as `export const Name = value`. Supported values:
  int/float/string literals, `true`/`false`, references to exported
  consts declared earlier in the input, unary `-`/`^` (emitted as `~`),
  and the operators `+ - * & | ^ << >>`. Operator expressions preserve
  Go's grouping with explicit parens (Go and TS precedence tables
  differ); associative chains like `A | B | C` stay flat. Legacy `017`
  octal re-emits as decimal. Not supported (rejected with the fix in
  the error): `iota` (gents renders expressions without evaluating them — write
  explicit values), forward references (TS `const` cannot
  forward-reference), and division/`%`/`&^` (semantics differ between
  Go and TS — precompute the value).
- **JSDoc passthrough + source provenance.** Go doc comments on marked
  structs, their fields (doc comment or trailing same-line comment),
  and const specs are emitted as JSDoc blocks — editor hover docs now
  come straight from the Go source. Every interface and const block
  also gets a `(source: file.go)` line naming the declaring file
  (basename only, so output stays byte-stable under line-number churn).
  Directive lines (`//gents:export`, `//gents:map`) never leak into
  the output.
- **Per-field `ts:"..."` tag override.** Replaces the emitted TS type
  for one field — e.g. ``EntryType string `json:"entry_type"
  ts:"'manual' | 'automatic'"` `` narrows a Go string to a literal
  union. The tag is a full escape hatch: it works even on Go types
  gents cannot map on its own. Factory zeros are inferred from the TS
  expression. Combining with `json:",string"` is rejected (the override
  already dictates the final type).
- **`-check` CLI flag.** Regenerates in memory and compares against
  `-out`: exit 0 when identical, exit 1 with a "re-run gents" message
  when stale or missing. Writes nothing. Wire it into CI or a test
  script to catch "edited the struct, forgot to `go generate`".
- **Near-miss marker guard.** A comment that is exactly the marker with
  stray whitespace (`// gents:export`, or `// gents:map A=B` with a
  parseable spec) is now rejected instead of silently ignored —
  previously the marked struct just quietly vanished from the output.
- **Literal-type zero inference.** `inferZero` now handles TS literal
  types and literal unions (`'manual' | 'automatic'` → `'manual'`,
  `0 | 1` → `0`) for `-map`, `//gents:map`, and the `ts` tag alike.
- **`UPDATE_GOLDEN=1 go test ./...`** rewrites every golden fixture
  from current emitter output. Review the git diff before trusting it.

- **Embedded struct flattening.** An untagged embedded field (`type Foo
  struct { Base }`) now inlines `Base`'s exported fields onto `Foo`,
  matching `encoding/json.Marshal`'s wire shape. Chains flatten
  recursively; `*Base` flattens the same way but marks every contributed
  field optional. Dominant-field resolution implements
  `encoding/json`'s rules: least-nested wins, and within the minimum
  depth a tagged contribution wins over an untagged one. Genuine
  ambiguities fail with the source position of each surviving
  contribution — `encoding/json` would silently drop all of them, but
  silently dropping is the kind of thing gents refuses to do.
  Cross-package embedding (`gorm.Model`, etc.) is still rejected: gents
  doesn't load external packages. Workarounds live in the error message.
- **MarshalJSON-inheritance guard for embedding.** If any type in the
  flatten chain declares `MarshalJSON`, gents fails rather than
  emitting a wire-wrong shape — same safety net as the named-alias
  case.
- **Embedded fields: two tagged patterns work.** `Base `json:"-"``
  silently skips the embedded field. `Base `json:"name"`` emits the
  embedded type as a nested object under that key.
- **Overwrite guardrail**: the CLI refuses to overwrite an existing
  `-out` file whose first line isn't the gents-generated header. Pass
  `-force` to override. Protects hand-written `.ts` files from
  accidental clobbering.
- **`json:",string"` tag modifier supported**: numeric and boolean
  fields tagged `,string` emit as TS `string` with appropriate zero
  values (`'0'`, `'false'`, or `null` for nullable forms). Matches
  `encoding/json` wire behavior, unblocking the common `int64,string`
  pattern for JS-safe IDs. Applied to `string`/`[]byte`/`time.Time`
  or any non-numeric, non-boolean type, gents fails with a clear
  message — matches `encoding/json`'s own rule that those combinations
  are either ill-defined or silently ignored.
- **`json:",omitzero"` tag modifier supported** (Go 1.24+). Treated
  identically to `omitempty` for TS emission (optional field + factory
  omits it); the difference between the two flags is Go-side only.
- **Errors, never panics, at the library boundary.** `Generate` /
  `GenerateDir` report every diagnostic (unsupported type, malformed
  tag, collision) as a returned error with a `file:line` prefix — the
  public API never panics on bad input. Internally the recursive
  collectors still abort via panic (the `encoding/json` pattern); a
  deferred recover at the API boundary converts deliberate diagnostics
  to errors and re-panics everything else, so a genuine bug in gents
  crashes with its full stack trace. The CLI prints diagnostics as
  single-line stderr messages with exit code 1 — no Go stack traces
  leaking through `//go:generate` pipelines.
- **`-out` parent directories auto-created**. Writing to
  `../client/types/foo.ts` now works on first run; previously it
  failed with "no such file or directory" if the intermediate dirs
  didn't exist.
- **GitHub Actions CI**: `go vet ./...`, `gofmt -l .`, and
  `go test -race ./...` on push to `main` and all PRs.
- **Cross-file-reference error nudges users toward bundle mode**. The
  "unsupported named type" error includes a hint pointing at
  `GenerateDir` / directory-mode `-in`. Single-file users hitting a
  sibling reference no longer have to find the docs themselves.
- **`//gents:export` on non-struct types fails loudly** (previously
  silently skipped). Applies to type aliases (`type Alias = Something`),
  interfaces, named primitives, and generic instantiations. Matches
  principle 7 ("fail loud on the impossible") — users who explicitly
  marked a type get a clear error instead of confused empty output.
- **In-file named aliases auto-resolve.** `type UserID string` (plus
  chains like `type A B; type B string`) no longer require `-map` —
  gents walks the alias to its underlying primitive type. Safety net:
  if the type declares a `MarshalJSON` method in the scanned input,
  gents fails with a pointer to `-map` rather than guessing wrong,
  since `MarshalJSON` overrides the wire shape. Cycles are detected
  and rejected. Cross-package types still need `-map` —
  auto-resolution is in-scanned-input only.
- **`//gents:map GoType=TSType` source directive.** Co-locate type
  mappings with the code that uses them instead of bloating
  `//go:generate` lines. Directives are global across the bundle:
  declare once, apply everywhere. Precedence: CLI `-map` > directive
  > built-in. Conflicting directives across files (same Go type,
  different TS type) fail with both source locations. Malformed
  directives also fail with a clear format hint.
- **Custom type mappings** via repeatable CLI `-map GoType=TSType` and
  `Options.TypeMap` library field. Unblocks third-party types
  (`uuid.UUID`, `decimal.Decimal`), named primitive aliases
  (`type MyString string`), and arbitrary qualified selectors that
  previously panicked. User mappings take precedence over built-ins,
  so `-map time.Time=Date|null` overrides the default. Factory zeros
  are inferred from the TS type; types whose zero cannot be inferred
  (e.g. `Date` alone, `Active | Pending`) fail at emit time with a
  suggestion to add `| null`. Collision detection fires if a mapped
  TS name matches a generated interface name.

### Fixed

- **Type-map collision check compared the wrong namespace.** It matched
  user-mapped TS types against *stripped factory base names* instead of
  the verbatim interface names actually emitted. Under `-strip` this
  missed real collisions (`-map X=tFoo` while emitting interface
  `tFoo`) and false-flagged non-collisions (`-map X=Foo` when only the
  *function* `newFoo` exists — value space, where a type expression
  cannot collide). The check now compares against interface names.

### Changed (breaking, pre-release)

- **`Options` simplified**: `Strip string, StripSet bool` → `Strip string`.
  Zero value (`Options{}`) means no stripping. The `StripSet` sentinel is
  gone. Callers who explicitly passed `StripSet: true` can drop it.
- **`-strip` is factory-only.** Stripping no longer rewrites the TS
  interface name — only the factory function name. So `tFoo` with
  `-strip=t` emits `interface tFoo` and `function newFoo()`, not
  `interface Foo` and `function newFoo()`. Rationale: the interface
  name is the wire shape's identity (closest TS analogue to a Go type
  name), while the factory name is a JS-side construction helper where
  the convention `newFoo()` reads better without the Go-only prefix.
  Two different naming targets, two different rules — uniform stripping
  conflated them. Restores the design doc §2.4 contract that the v0.1
  pre-release implementation drifted away from. **Migration:** if you
  relied on stripping the interface name, consumers' TS imports must
  switch back to the verbatim Go name (e.g. `import { Foo }` →
  `import { tFoo }`).
- **CLI `-strip` default changed back to `"t"`.** Restores the v0.1.0
  default. Library `Options.Strip` zero value remains `""` (no
  stripping) — the CLI provides the convention default; the library
  stays explicit. Existing `//go:generate` lines that added `-strip=t`
  during the brief `""` window can drop it; lines that explicitly
  passed `-strip=""` to opt OUT of stripping should keep that.
- **`time.Time` factory zero changed**: `'0001-01-01T00:00:00Z'` →
  `''`. Matches the design doc §2.3 rule "TS type `string` → factory
  zero `''`" uniformly. The old value reflected what
  `encoding/json.Marshal(time.Time{})` actually produces, but the
  factory zero is consumed by the SPA before any wire round-trip — it
  scaffolds an empty form, not a real timestamp. Empty string is the
  falsy/"no value yet" sentinel JS code already uses for unset string
  fields. `time.Duration` and `*time.Time` are unchanged (`0` and
  `null` respectively).
- **CLI `-in` unified**: accepts a file *or* a directory. `-in-dir` is
  gone. gents stat's the path and dispatches to single-file or bundle
  mode automatically. Matches how `gofmt`, `go vet`, and other Go tools
  treat their positional arguments.
- **`-in` defaults to the current directory** when omitted. `gents -out
  types.ts` now does a full bundle of `.`. Only `-out` is strictly
  required.
- **`testdata/` directories are skipped during recursive scan** (same
  rule as `_`- and `.`-prefixed entries). Matches Go tooling
  convention (`go test ./...` behavior). Pointing `-in` directly at
  `./testdata` still works — the skip only applies to nested directories
  encountered during a walk.

## [0.1.0] — initial

### Added

- Initial v0.1 implementation: parser, emitter, CLI, library API.
- Support for primitives, `time.Time`, `time.Duration`, `json.RawMessage`,
  `any`/`interface{}`, `[]byte` (base64), slices, string-keyed maps,
  pointers, sibling cross-struct references.
- `-strip` flag applied uniformly to interface and factory names.
- Quoted field names for JSON keys that aren't valid TS identifiers.
- Bundle mode: `GenerateDir` library function and `-in-dir` CLI flag
  scan a directory tree recursively and emit a single TS file covering
  every marked struct, with cross-file references resolving naturally.
  Originally planned for v0.2; pulled into v0.1.
- Name-collision detection across bundled files (Go name duplicated, or
  two different structs stripping to the same TS name) panics with both
  source locations.
