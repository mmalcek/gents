package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeWrite_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.ts")
	data := []byte(generatedHeader + "\n\nexport interface X {}\n")
	if err := safeWrite(path, data, false); err != nil {
		t.Fatalf("safeWrite: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("content mismatch")
	}
}

func TestSafeWrite_OverwriteGeneratedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.ts")
	// Seed with a gents-generated file.
	if err := os.WriteFile(path, []byte(generatedHeader+"\n\nexport interface Old {}\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Overwrite without -force should succeed because header matches.
	newData := []byte(generatedHeader + "\n\nexport interface New {}\n")
	if err := safeWrite(path, newData, false); err != nil {
		t.Fatalf("safeWrite: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(newData) {
		t.Fatalf("expected overwrite, got %q", got)
	}
}

func TestSafeWrite_RefusesHandWrittenFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "handwritten.ts")
	// Seed with a non-gents file.
	orig := []byte("// someone's actual TypeScript\nexport const x = 1\n")
	if err := os.WriteFile(path, orig, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := safeWrite(path, []byte(generatedHeader+"\n"), false)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not a gents-generated file") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "-force") {
		t.Fatalf("error should mention -force escape hatch, got: %v", err)
	}
	// Original file must be untouched.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(orig) {
		t.Fatalf("file was modified despite refusal: %q", got)
	}
}

func TestSafeWrite_ForceOverridesGuardrail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "handwritten.ts")
	if err := os.WriteFile(path, []byte("// hand-written\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	newData := []byte(generatedHeader + "\n")
	if err := safeWrite(path, newData, true); err != nil {
		t.Fatalf("safeWrite with force: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(newData) {
		t.Fatalf("force should have overwritten, got %q", got)
	}
}

func TestSafeWrite_EmptyFileRefusedWithoutForce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.ts")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := safeWrite(path, []byte(generatedHeader+"\n"), false); err == nil {
		t.Fatalf("expected refusal on empty pre-existing file")
	}
}

// TestSafeWrite_CreatesParentDir — writing to a/b/c/foo.ts creates the
// a/b/c/ path if it doesn't exist, so first-time users don't hit a
// confusing "no such file or directory" error.
func TestSafeWrite_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deeply", "foo.ts")
	data := []byte(generatedHeader + "\n")
	if err := safeWrite(path, data, false); err != nil {
		t.Fatalf("safeWrite: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("content mismatch")
	}
}

// TestRun_RecoversFromLibraryPanic — a library panic on the way out of
// Generate must become a single-line stderr message with exit code 1,
// not a Go stack trace. This is the production-grade UX guardrail.
func TestRun_RecoversFromLibraryPanic(t *testing.T) {
	// Resolve the repo-root path so we can reference testdata fixtures
	// from inside cmd/gents where `go test` cwd's us.
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	input := filepath.Join(repoRoot, "testdata", "panic_chan", "input.go")
	out := filepath.Join(t.TempDir(), "out.ts")

	var stderr strings.Builder
	code := run([]string{"-in", input, "-out", out}, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	msg := stderr.String()
	if !strings.Contains(msg, "channel types cannot be marshaled") {
		t.Fatalf("stderr missing the panic's error message:\n%s", msg)
	}
	if strings.Contains(msg, "goroutine") || strings.Contains(msg, "runtime.gopanic") {
		t.Fatalf("stderr contains a Go stack trace — recover isn't working:\n%s", msg)
	}
}

// TestRun_Success — end-to-end sanity: a valid single-file invocation
// returns 0 and writes the expected output.
func TestRun_Success(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	input := filepath.Join(repoRoot, "testdata", "simple", "input.go")
	out := filepath.Join(t.TempDir(), "out.ts")

	var stderr strings.Builder
	code := run([]string{"-in", input, "-out", out}, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d; stderr:\n%s", code, stderr.String())
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	want, err := os.ReadFile(filepath.Join(repoRoot, "testdata", "simple", "expected.ts"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("output mismatch")
	}
}

// TestRun_MissingOutFlag — -out is the only required flag. Absence
// produces a clean error mentioning -out, not a stack trace.
func TestRun_MissingOutFlag(t *testing.T) {
	var stderr strings.Builder
	code := run([]string{}, &stderr)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "-out") {
		t.Fatalf("stderr should mention -out requirement, got:\n%s", stderr.String())
	}
}

// TestRun_DefaultsToCurrentDir — omitting -in processes the current
// working directory (bundle mode). Tested by cd'ing into a fixture
// dir that contains marked structs and asserting the output matches
// what GenerateDir on that dir produces.
func TestRun_DefaultsToCurrentDir(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	fixtureDir := filepath.Join(repoRoot, "testdata", "multi_file_bundle", "input")
	out := filepath.Join(t.TempDir(), "out.ts")

	// chdir into the fixture dir so -in defaults to "." = the fixture.
	oldWD, _ := os.Getwd()
	if err := os.Chdir(fixtureDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(oldWD)

	var stderr strings.Builder
	code := run([]string{"-out", out}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr:\n%s", code, stderr.String())
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	want, err := os.ReadFile(filepath.Join(repoRoot, "testdata", "multi_file_bundle", "expected.ts"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("output mismatch")
	}
}

// TestRun_DirPath — passing a directory via -in goes into bundle mode.
func TestRun_DirPath(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	input := filepath.Join(repoRoot, "testdata", "multi_file_bundle", "input")
	out := filepath.Join(t.TempDir(), "out.ts")

	var stderr strings.Builder
	code := run([]string{"-in", input, "-out", out}, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr:\n%s", code, stderr.String())
	}
	want, _ := os.ReadFile(filepath.Join(repoRoot, "testdata", "multi_file_bundle", "expected.ts"))
	got, _ := os.ReadFile(out)
	if string(got) != string(want) {
		t.Fatalf("output mismatch")
	}
}

// TestRun_NonexistentPath — clear error, no stack trace.
func TestRun_NonexistentPath(t *testing.T) {
	var stderr strings.Builder
	code := run([]string{"-in", "/definitely/does/not/exist.go", "-out", "/tmp/x.ts"}, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if strings.Contains(stderr.String(), "goroutine") {
		t.Fatalf("stack trace leaked:\n%s", stderr.String())
	}
}
