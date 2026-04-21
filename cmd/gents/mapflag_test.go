package main

import (
	"strings"
	"testing"
)

func TestMapFlag_Set_Valid(t *testing.T) {
	m := mapFlag{}
	if err := m.Set("uuid.UUID=string"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m["uuid.UUID"] != "string" {
		t.Fatalf("expected uuid.UUID=string, got %q", m["uuid.UUID"])
	}
	if err := m.Set("MyString=string | null"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m["MyString"] != "string | null" {
		t.Fatalf("expected MyString=string | null, got %q", m["MyString"])
	}
}

func TestMapFlag_Set_TrimsWhitespace(t *testing.T) {
	m := mapFlag{}
	if err := m.Set("  uuid.UUID  =  string  "); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m["uuid.UUID"] != "string" {
		t.Fatalf("expected trimmed values, got key=%q val=%q", "uuid.UUID", m["uuid.UUID"])
	}
}

func TestMapFlag_Set_Malformed(t *testing.T) {
	cases := []string{
		"no-equals-sign",
		"=missing-key",
		"missing-value=",
		"",
		"   =   ",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			m := mapFlag{}
			err := m.Set(c)
			if err == nil {
				t.Fatalf("expected error for %q, got nil", c)
			}
			if !strings.Contains(err.Error(), "GoType=TSType") {
				t.Fatalf("error %q missing expected shape hint", err)
			}
		})
	}
}

func TestMapFlag_Set_Duplicate(t *testing.T) {
	m := mapFlag{}
	if err := m.Set("uuid.UUID=string"); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	err := m.Set("uuid.UUID=number")
	if err == nil {
		t.Fatalf("expected duplicate error, got nil")
	}
	if !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("duplicate error missing expected phrase: %v", err)
	}
	// Original value must be preserved.
	if m["uuid.UUID"] != "string" {
		t.Fatalf("duplicate Set clobbered original value: got %q", m["uuid.UUID"])
	}
}

func TestMapFlag_String_Sorted(t *testing.T) {
	m := mapFlag{
		"z.Z":    "string",
		"a.A":    "number",
		"middle": "boolean",
	}
	got := m.String()
	want := "a.A=number,middle=boolean,z.Z=string"
	if got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
