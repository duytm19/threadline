package catalog

import (
	"regexp"
	"strings"
)

// Allowed SKU syntax: 3 to 64 alphanumeric characters, underscores, or hyphens.
var skuRegex = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{2,63}$`)

// SKU represents a stock keeping unit identifier value object.
type SKU struct {
	value string
}

// NewSKU validates and normalizes a SKU string.
func NewSKU(raw string) (SKU, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(raw))
	if trimmed == "" {
		return SKU{}, ErrEmptySKU
	}
	if !skuRegex.MatchString(trimmed) {
		return SKU{}, ErrInvalidSKU
	}
	return SKU{value: trimmed}, nil
}

// MustNewSKU constructs a SKU or panics if invalid.
func MustNewSKU(raw string) SKU {
	s, err := NewSKU(raw)
	if err != nil {
		panic(err)
	}
	return s
}

// Value returns the normalized SKU string.
func (s SKU) Value() string {
	return s.value
}

// String implements fmt.Stringer.
func (s SKU) String() string {
	return s.value
}
