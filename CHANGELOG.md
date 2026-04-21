# Changelog

All notable changes to gents are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased] — v0.1.1

### Changed (breaking, pre-release)

- **`Options` simplified**: `Strip string, StripSet bool` → `Strip string`.
  Zero value (`Options{}`) means no stripping. The `StripSet` sentinel is
  gone. Callers who explicitly passed `StripSet: true` can drop it.
- **Default `-strip` changed**: `"t"` → `""` (no stripping). gents takes
  no position on Go naming convention; if you want a prefix stripped,
  opt in by setting `-strip` explicitly. Existing `//go:generate` lines
  that relied on the old default should add `-strip=t` to preserve
  behavior.

### Added

- **Overwrite guardrail**: the CLI refuses to overwrite an existing
  `-out` file whose first line isn't the gents-generated header. Pass
  `-force` to override. Protects hand-written `.ts` files from
  accidental clobbering.
- **`json:",string"` tag modifier supported**: numeric and boolean
  fields tagged `,string` emit as TS `string` with appropriate zero
  values (`'0'`, `'false'`, or `null` for nullable forms). Matches
  `encoding/json` wire behavior, unblocking the common `int64,string`
  pattern for JS-safe IDs. Applied to `string`/`[]byte`/`time.Time`
  or any non-numeric, non-boolean type, gents panics with a clear
  message — matches `encoding/json`'s own rule that those combinations
  are either ill-defined or silently ignored.
- **`json:",omitzero"` tag modifier supported** (Go 1.24+). Treated
  identically to `omitempty` for TS emission (optional field + factory
  omits it); the difference between the two flags is Go-side only.
- **CLI panic recovery**: panics from the library (unsupported type,
  malformed tag, collision) are now converted to single-line stderr
  messages with exit code 1. No more Go stack traces leaking through
  `//go:generate` pipelines. The CLI still wraps everything in
  `defer recover()`, so a runtime bug in gents itself would still
  surface cleanly instead of nuking a build log.
- **`-out` parent directories auto-created**. Writing to
  `../client/types/foo.ts` now works on first run; previously it
  failed with "no such file or directory" if the intermediate dirs
  didn't exist.
- **GitHub Actions CI**: `go vet ./...` and `go test -race ./...` on
  push to `main` and all PRs. 15 lines in `.github/workflows/test.yml`.
- **Cross-file-reference error nudges users toward bundle mode**. The
  "unsupported named type" panic now includes a hint: "If X lives in
  another file, switch to bundle mode: gents -in-dir <dir>". Single-
  file users hitting a sibling reference no longer have to find the
  docs themselves.
- **`//gents:export` on non-struct types now panics loudly** (previously
  silently skipped). Applies to type aliases (`type Alias = Something`),
  interfaces, named primitives, and generic instantiations. Matches
  principle 7 ("fail loud on the impossible") — users who explicitly
  marked a type get a clear error instead of confused empty output.

### Changed (breaking, pre-release)

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

### Added (continued)

- **Embedded fields: two patterns now work.** `Base `json:"-"`` silently
  skips the embedded field (previously panicked). `Base `json:"name"``
  emits the embedded type as a nested object under that key
  (previously panicked). Untagged embedded fields still panic because
  default flattening remains a v0.2 feature — but the error now lists
  the three workarounds (tag with name, tag with `-`, rewrite as
  explicit named field).
- **In-file named aliases auto-resolve.** `type UserID string` (plus
  chains like `type A B; type B string`) no longer require `-map` —
  gents walks the alias to its underlying primitive type. Safety net:
  if the type declares a `MarshalJSON` method in the scanned input,
  gents panics with a pointer to `-map` rather than guessing wrong,
  since `MarshalJSON` overrides the wire shape. Cycles are detected
  and panicked on. Cross-package types still need `-map` — auto-
  resolution is in-scanned-input only.
- **Custom type mappings** via repeatable CLI `-map GoType=TSType` and
  `Options.TypeMap` library field. Unblocks third-party types
  (`uuid.UUID`, `decimal.Decimal`), named primitive aliases
  (`type MyString string`), and arbitrary qualified selectors that
  previously panicked. User mappings take precedence over built-ins,
  so `-map time.Time=Date|null` overrides the default. Factory zeros
  are inferred from the TS type; types whose zero cannot be inferred
  (e.g. `Date` alone, `Active | Pending`) panic at emit time with a
  suggestion to add `| null`. Collision detection fires if a mapped
  TS name matches a generated interface name.

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
