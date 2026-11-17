package catalog_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/threadline/services/catalog/internal/domain/catalog"
)

func TestSKU_Validation(t *testing.T) {
	// Empty SKU
	_, err := catalog.NewSKU("")
	assert.ErrorIs(t, err, catalog.ErrEmptySKU)

	_, err = catalog.NewSKU("   ")
	assert.ErrorIs(t, err, catalog.ErrEmptySKU)

	// Too short (< 3 characters)
	_, err = catalog.NewSKU("AB")
	assert.ErrorIs(t, err, catalog.ErrInvalidSKU)

	// Invalid characters
	_, err = catalog.NewSKU("SKU#123")
	assert.ErrorIs(t, err, catalog.ErrInvalidSKU)

	// Valid SKU normalized to uppercase
	sku, err := catalog.NewSKU("dress-red-38")
	require.NoError(t, err)
	assert.Equal(t, "DRESS-RED-38", sku.Value())
	assert.Equal(t, "DRESS-RED-38", sku.String())

	// MustNewSKU
	assert.NotPanics(t, func() {
		catalog.MustNewSKU("VALID-SKU-1")
	})
	assert.Panics(t, func() {
		catalog.MustNewSKU("!!")
	})
}
