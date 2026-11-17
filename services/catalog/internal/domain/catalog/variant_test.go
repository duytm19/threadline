package catalog_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/threadline/services/catalog/internal/domain/catalog"
)

func TestNewProductVariant_Success(t *testing.T) {
	now := time.Now().UTC()
	sku := catalog.MustNewSKU("SHIRT-BLU-S")
	price := catalog.MustNewMoney(2999, "USD")
	attrs := catalog.NewAttributes(map[string]any{"cotton_pct": 100})

	v, err := catalog.NewProductVariant(
		"var-1",
		"prod-1",
		sku,
		"Cotton Oxford Shirt - Blue / S",
		"S",
		"Blue",
		price,
		"8931234567890",
		200,
		attrs,
		now,
	)
	require.NoError(t, err)

	assert.Equal(t, "var-1", v.ID())
	assert.Equal(t, "prod-1", v.ProductID())
	assert.Equal(t, "SHIRT-BLU-S", v.SKU().Value())
	assert.Equal(t, "Cotton Oxford Shirt - Blue / S", v.Name())
	assert.Equal(t, "S", v.Size())
	assert.Equal(t, "Blue", v.Color())
	assert.Equal(t, int64(2999), v.Price().Amount())
	assert.Equal(t, "USD", v.Price().Currency())
	assert.Equal(t, "8931234567890", v.Barcode())
	assert.Equal(t, 200, v.WeightGrams())
	assert.True(t, v.IsActive())
	assert.Equal(t, now, v.CreatedAt())
	assert.Equal(t, now, v.UpdatedAt())
}

func TestNewProductVariant_ValidationErrors(t *testing.T) {
	now := time.Now().UTC()
	sku := catalog.MustNewSKU("SKU-123")
	price := catalog.MustNewMoney(1000, "USD")
	emptyPrice := catalog.Money{}
	attrs := catalog.NewAttributes(nil)

	tests := []struct {
		name        string
		id          string
		productID   string
		sku         catalog.SKU
		price       catalog.Money
		weight      int
		expectedErr error
	}{
		{"empty id", "", "p1", sku, price, 100, catalog.ErrEmptyVariantID},
		{"empty productID", "v1", "", sku, price, 100, catalog.ErrEmptyProductID},
		{"empty sku", "v1", "p1", catalog.SKU{}, price, 100, catalog.ErrEmptySKU},
		{"uninitialized price", "v1", "p1", sku, emptyPrice, 100, catalog.ErrInvalidCurrency},
		{"negative weight", "v1", "p1", sku, price, -5, catalog.ErrNegativeWeight},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := catalog.NewProductVariant(tt.id, tt.productID, tt.sku, "name", "M", "Red", tt.price, "", tt.weight, attrs, now)
			assert.ErrorIs(t, err, tt.expectedErr)
		})
	}
}

func TestProductVariant_UpdatePrice(t *testing.T) {
	now := time.Now().UTC()
	v := makeTestVariant(t, "v1", "p1", "SKU-TEST", 2000)

	// Valid price update
	newPrice := catalog.MustNewMoney(2500, "USD")
	err := v.UpdatePrice(newPrice, now)
	require.NoError(t, err)
	assert.Equal(t, int64(2500), v.Price().Amount())

	// Invalid empty currency price
	err = v.UpdatePrice(catalog.Money{}, now)
	assert.ErrorIs(t, err, catalog.ErrInvalidCurrency)
}

func TestProductVariant_UpdateDetails(t *testing.T) {
	now := time.Now().UTC()
	v := makeTestVariant(t, "v1", "p1", "SKU-TEST", 2000)

	err := v.UpdateDetails("New Name", "L", "Navy", "999888", -10, catalog.NewAttributes(nil), now)
	assert.ErrorIs(t, err, catalog.ErrNegativeWeight)

	err = v.UpdateDetails("New Name", "L", "Navy", "999888", 350, catalog.NewAttributes(map[string]any{"fit": "slim"}), now)
	require.NoError(t, err)
	assert.Equal(t, "New Name", v.Name())
	assert.Equal(t, "L", v.Size())
	assert.Equal(t, "Navy", v.Color())
	assert.Equal(t, "999888", v.Barcode())
	assert.Equal(t, 350, v.WeightGrams())
}

func TestProductVariant_Activate_Deactivate(t *testing.T) {
	now := time.Now().UTC()
	v := makeTestVariant(t, "v1", "p1", "SKU-TEST", 2000)
	assert.True(t, v.IsActive())

	v.Deactivate(now)
	assert.False(t, v.IsActive())

	// Idempotent
	v.Deactivate(now)
	assert.False(t, v.IsActive())

	v.Activate(now)
	assert.True(t, v.IsActive())

	// Idempotent
	v.Activate(now)
	assert.True(t, v.IsActive())
}

func TestReconstituteProductVariant(t *testing.T) {
	now := time.Now().UTC()
	sku := catalog.MustNewSKU("SKU-RECON")
	price := catalog.MustNewMoney(5000, "VND")
	attrs := catalog.NewAttributes(map[string]any{"season": "spring"})

	v := catalog.ReconstituteProductVariant("v1", "p1", sku, "Name", "M", "Green", price, "123", 150, attrs, false, now, now)
	assert.Equal(t, "v1", v.ID())
	assert.False(t, v.IsActive())
	assert.Equal(t, "SKU-RECON", v.SKU().Value())
	assert.NotEmpty(t, v.Attributes().Map())
}

func TestProductVariant_ZeroTimeFallback(t *testing.T) {
	sku := catalog.MustNewSKU("SKU-FALLBACK")
	price := catalog.MustNewMoney(100, "USD")
	v, err := catalog.NewProductVariant("v1", "p1", sku, "N", "M", "C", price, "", 10, catalog.NewAttributes(nil), time.Time{})
	require.NoError(t, err)
	assert.False(t, v.CreatedAt().IsZero())
	assert.False(t, v.UpdatedAt().IsZero())

	err = v.UpdatePrice(price, time.Time{})
	require.NoError(t, err)

	err = v.UpdateDetails("N", "M", "C", "", 20, catalog.NewAttributes(nil), time.Time{})
	require.NoError(t, err)

	v.Deactivate(time.Time{})
	assert.False(t, v.IsActive())

	v.Activate(time.Time{})
	assert.True(t, v.IsActive())
}
