package main

import (
	"fmt"
	"sort"
	"strings"
)

// mapFlag implements flag.Value for the repeatable -map option.
// Each -map=GoType=TSType invocation adds one entry. Duplicate Go-type
// keys return an error so users notice typos rather than silently
// losing the earlier mapping.
type mapFlag map[string]string

func (m mapFlag) String() string {
	if len(m) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func (m mapFlag) Set(value string) error {
	idx := strings.Index(value, "=")
	if idx <= 0 || idx == len(value)-1 {
		return fmt.Errorf("-map value must be in the form GoType=TSType, got %q", value)
	}
	goType := strings.TrimSpace(value[:idx])
	tsType := strings.TrimSpace(value[idx+1:])
	if goType == "" || tsType == "" {
		return fmt.Errorf("-map value must be in the form GoType=TSType, got %q", value)
	}
	if _, dup := m[goType]; dup {
		return fmt.Errorf("-map %q specified more than once", goType)
	}
	m[goType] = tsType
	return nil
}
