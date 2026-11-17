package catalog_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/threadline/services/catalog/internal/domain/catalog"
)

func makeTestVariant(t *testing.T, id, productID, skuStr string, amount int64) catalog.ProductVariant {
	sku, err := catalog.NewSKU(skuStr)
	require.NoError(t, err)
	price, err := catalog.NewMoney(amount, "USD")
	require.NoError(t, err)
	attrs := catalog.NewAttributes(map[string]any{"material": "silk"})

	v, err := catalog.NewProductVariant(id, productID, sku, "Silk Dress", "M", "Red", price, "123456789", 250, attrs, time.Now().UTC())
	require.NoError(t, err)
	return *v
}

func TestNewProduct_Success(t *testing.T) {
	now := time.Now().UTC()
	attrs := catalog.NewAttributes(map[string]any{"care": "hand wash"})

	p, err := catalog.NewProduct("prod-1", "tenant-1", "Silk Evening Gown", "silk-evening-gown", "A luxurious gown", "cat-1", "Threadline", "FW26", attrs, now)
	require.NoError(t, err)

	assert.Equal(t, "prod-1", p.ID())
	assert.Equal(t, "tenant-1", p.TenantID())
	assert.Equal(t, "Silk Evening Gown", p.Name())
	assert.Equal(t, "silk-evening-gown", p.Slug())
	assert.Equal(t, "A luxurious gown", p.Description())
	assert.Equal(t, "cat-1", p.CategoryID())
	assert.Equal(t, "Threadline", p.Brand())
	assert.Equal(t, "FW26", p.Season())
	assert.False(t, p.IsArchived())
	assert.Empty(t, p.Variants())

	events := p.PopEvents()
	require.Len(t, events, 1)
	assert.Equal(t, "catalog.product.created", events[0].EventName())
	assert.Equal(t, now, events[0].OccurredAt())
}

func TestNewProduct_ValidationErrors(t *testing.T) {
	now := time.Now().UTC()
	attrs := catalog.NewAttributes(nil)

	tests := []struct {
		name        string
		id          string
		tenantID    string
		pName       string
		slug        string
		categoryID  string
		expectedErr error
	}{
		{"empty id", "", "t-1", "Name", "valid-slug", "cat-1", catalog.ErrEmptyProductID},
		{"empty tenant", "p-1", "", "Name", "valid-slug", "cat-1", catalog.ErrEmptyTenantID},
		{"empty name", "p-1", "t-1", "", "valid-slug", "cat-1", catalog.ErrEmptyProductName},
		{"empty slug", "p-1", "t-1", "Name", "", "cat-1", catalog.ErrInvalidProductSlug},
		{"invalid slug", "p-1", "t-1", "Name", "Invalid Slug!", "cat-1", catalog.ErrInvalidProductSlug},
		{"empty category", "p-1", "t-1", "Name", "valid-slug", "", catalog.ErrEmptyCategoryIDOnProd},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := catalog.NewProduct(tt.id, tt.tenantID, tt.pName, tt.slug, "desc", tt.categoryID, "b", "s", attrs, now)
			assert.ErrorIs(t, err, tt.expectedErr)
		})
	}
}

func TestProduct_AddVariant_Invariants(t *testing.T) {
	now := time.Now().UTC()
	p, err := catalog.NewProduct("p1", "t1", "Dress", "dress", "desc", "c1", "b", "s", catalog.NewAttributes(nil), now)
	require.NoError(t, err)
	p.PopEvents()

	v1 := makeTestVariant(t, "v1", "p1", "SKU-RED-M", 10000)
	v2 := makeTestVariant(t, "v2", "p1", "SKU-RED-L", 10000)

	// Successfully add v1
	err = p.AddVariant(v1, now)
	require.NoError(t, err)
	assert.Len(t, p.Variants(), 1)
	assert.Len(t, p.ActiveVariants(), 1)

	// Adding same variant ID or SKU must fail with ErrDuplicateSKU
	v1DuplicateSKU := makeTestVariant(t, "v3", "p1", "SKU-RED-M", 12000)
	err = p.AddVariant(v1DuplicateSKU, now)
	assert.ErrorIs(t, err, catalog.ErrDuplicateSKU)

	vDuplicateID := makeTestVariant(t, "v1", "p1", "SKU-RED-XL", 12000)
	err = p.AddVariant(vDuplicateID, now)
	assert.ErrorIs(t, err, catalog.ErrDuplicateSKU)

	// Add second variant successfully
	err = p.AddVariant(v2, now)
	require.NoError(t, err)
	assert.Len(t, p.Variants(), 2)

	events := p.PopEvents()
	require.Len(t, events, 2)
	assert.Equal(t, "catalog.variant.added", events[0].EventName())
	assert.Equal(t, "catalog.variant.added", events[1].EventName())
}

func TestProduct_UpdateVariant_Invariants(t *testing.T) {
	now := time.Now().UTC()
	p, err := catalog.NewProduct("p1", "t1", "Dress", "dress", "desc", "c1", "b", "s", catalog.NewAttributes(nil), now)
	require.NoError(t, err)

	v1 := makeTestVariant(t, "v1", "p1", "SKU-RED-M", 10000)
	v2 := makeTestVariant(t, "v2", "p1", "SKU-RED-L", 10000)
	require.NoError(t, p.AddVariant(v1, now))
	require.NoError(t, p.AddVariant(v2, now))
	p.PopEvents()

	// Update non-existent variant
	vNonExistent := makeTestVariant(t, "v999", "p1", "SKU-NEW", 10000)
	err = p.UpdateVariant(vNonExistent, now)
	assert.ErrorIs(t, err, catalog.ErrVariantNotFound)

	// Update v1 SKU to collide with v2 SKU
	v1Colliding := makeTestVariant(t, "v1", "p1", "SKU-RED-L", 15000)
	err = p.UpdateVariant(v1Colliding, now)
	assert.ErrorIs(t, err, catalog.ErrDuplicateSKU)

	// Valid update to price and attributes
	v1Updated := makeTestVariant(t, "v1", "p1", "SKU-RED-M", 15000)
	err = p.UpdateVariant(v1Updated, now)
	require.NoError(t, err)

	updatedV, err := p.VariantByID("v1")
	require.NoError(t, err)
	assert.Equal(t, int64(15000), updatedV.Price().Amount())

	events := p.PopEvents()
	require.Len(t, events, 1)
	assert.Equal(t, "catalog.variant.updated", events[0].EventName())
}

func TestProduct_RemoveVariant_Invariants(t *testing.T) {
	now := time.Now().UTC()
	p, err := catalog.NewProduct("p1", "t1", "Dress", "dress", "desc", "c1", "b", "s", catalog.NewAttributes(nil), now)
	require.NoError(t, err)

	v1 := makeTestVariant(t, "v1", "p1", "SKU-1", 1000)
	v2 := makeTestVariant(t, "v2", "p1", "SKU-2", 2000)
	require.NoError(t, p.AddVariant(v1, now))
	require.NoError(t, p.AddVariant(v2, now))

	// Remove non-existent variant
	err = p.RemoveVariant("v999", now)
	assert.ErrorIs(t, err, catalog.ErrVariantNotFound)

	// Publish product for tenant
	err = p.PublishForTenant("tenant-vn", now)
	require.NoError(t, err)

	// Removing one of two variants succeeds
	err = p.RemoveVariant("v1", now)
	require.NoError(t, err)
	assert.Len(t, p.Variants(), 1)

	// Removing the LAST active variant when product is published MUST FAIL
	err = p.RemoveVariant("v2", now)
	assert.ErrorIs(t, err, catalog.ErrCannotRemoveLastVariant)
	assert.Len(t, p.Variants(), 1)

	// Unpublish product -> now removing last variant is permitted
	err = p.UnpublishForTenant("tenant-vn", now)
	require.NoError(t, err)

	err = p.RemoveVariant("v2", now)
	require.NoError(t, err)
	assert.Empty(t, p.Variants())
}

func TestProduct_TenantPublication_Isolation(t *testing.T) {
	now := time.Now().UTC()
	p, err := catalog.NewProduct("PRD-1", "tenant-vn", "Linen Shirt", "linen-shirt", "100% linen", "cat-1", "Threadline", "SS26", catalog.NewAttributes(nil), now)
	require.NoError(t, err)

	// Invariant: Cannot publish product without active variants
	err = p.PublishForTenant("zalora-vn", now)
	assert.ErrorIs(t, err, catalog.ErrProductNoVariants)

	// Invariant: Tenant ID cannot be empty (fail-closed isolation)
	err = p.PublishForTenant("", now)
	assert.ErrorIs(t, err, catalog.ErrEmptyTenantID)

	// Add active variant
	v := makeTestVariant(t, "v1", "PRD-1", "SHIRT-WHT-L", 4500)
	require.NoError(t, p.AddVariant(v, now))
	p.PopEvents()

	// Scenario 2: Publish for zalora-vn, unpublished for iconic-au
	err = p.PublishForTenant("zalora-vn", now)
	require.NoError(t, err)

	assert.True(t, p.IsPublishedForTenant("zalora-vn"))
	assert.False(t, p.IsPublishedForTenant("iconic-au"))
	assert.False(t, p.IsPublishedForTenant(""))

	pubVN, exists := p.PublicationForTenant("zalora-vn")
	require.True(t, exists)
	assert.Equal(t, catalog.PublicationStatusPublished, pubVN.Status())
	assert.NotNil(t, pubVN.PublishedAt())
	assert.True(t, pubVN.IsPublished())

	// Unpublish for zalora-vn
	later := now.Add(time.Hour)
	err = p.UnpublishForTenant("zalora-vn", later)
	require.NoError(t, err)
	assert.False(t, p.IsPublishedForTenant("zalora-vn"))

	pubVNUnpub, exists := p.PublicationForTenant("zalora-vn")
	require.True(t, exists)
	assert.Equal(t, catalog.PublicationStatusUnpublished, pubVNUnpub.Status())
	assert.False(t, pubVNUnpub.IsPublished())
	// Preserves original publishedAt timestamp
	assert.Equal(t, pubVN.PublishedAt(), pubVNUnpub.PublishedAt())

	// Unpublish with empty tenant
	err = p.UnpublishForTenant("", later)
	assert.ErrorIs(t, err, catalog.ErrEmptyTenantID)
}

func TestProduct_Archive_Invariants(t *testing.T) {
	now := time.Now().UTC()
	p, err := catalog.NewProduct("p1", "t1", "Coat", "coat", "desc", "c1", "b", "s", catalog.NewAttributes(nil), now)
	require.NoError(t, err)

	v := makeTestVariant(t, "v1", "p1", "COAT-BLK-40", 25000)
	require.NoError(t, p.AddVariant(v, now))
	require.NoError(t, p.PublishForTenant("t1", now))
	assert.True(t, p.IsPublishedForTenant("t1"))

	// Archive product
	err = p.Archive(now)
	require.NoError(t, err)
	assert.True(t, p.IsArchived())
	assert.False(t, p.IsPublishedForTenant("t1"))

	// Invariants: Archived product cannot be modified, published, or have variants altered
	assert.ErrorIs(t, p.PublishForTenant("t1", now), catalog.ErrProductArchived)
	assert.ErrorIs(t, p.UnpublishForTenant("t1", now), catalog.ErrProductArchived)
	assert.ErrorIs(t, p.AddVariant(v, now), catalog.ErrProductArchived)
	assert.ErrorIs(t, p.UpdateVariant(v, now), catalog.ErrProductArchived)
	assert.ErrorIs(t, p.RemoveVariant("v1", now), catalog.ErrProductArchived)
	assert.ErrorIs(t, p.UpdateDetails("New", "desc", "c1", "b", "s", catalog.NewAttributes(nil), now), catalog.ErrProductArchived)

	// Idempotent archive call
	err = p.Archive(now)
	assert.NoError(t, err)
}

func TestProduct_Queries_And_Lookups(t *testing.T) {
	now := time.Now().UTC()
	p, err := catalog.NewProduct("p1", "t1", "Jeans", "jeans", "denim", "c1", "DenimCo", "SS26", catalog.NewAttributes(nil), now)
	require.NoError(t, err)

	v1 := makeTestVariant(t, "v1", "p1", "JEANS-30", 6000)
	v2 := makeTestVariant(t, "v2", "p1", "JEANS-32", 6000)
	v2.Deactivate(now)

	require.NoError(t, p.AddVariant(v1, now))
	require.NoError(t, p.AddVariant(v2, now))

	// By ID
	found, err := p.VariantByID("v1")
	require.NoError(t, err)
	assert.Equal(t, "JEANS-30", found.SKU().Value())

	_, err = p.VariantByID("v999")
	assert.ErrorIs(t, err, catalog.ErrVariantNotFound)

	// By SKU
	sku30 := catalog.MustNewSKU("JEANS-30")
	foundSKU, err := p.VariantBySKU(sku30)
	require.NoError(t, err)
	assert.Equal(t, "v1", foundSKU.ID())

	skuUnknown := catalog.MustNewSKU("UNKNOWN-SKU")
	_, err = p.VariantBySKU(skuUnknown)
	assert.ErrorIs(t, err, catalog.ErrVariantNotFound)

	// Active variants filter
	assert.Len(t, p.Variants(), 2)
	assert.Len(t, p.ActiveVariants(), 1)
	assert.Equal(t, "v1", p.ActiveVariants()[0].ID())

	// Defensive copy of publications
	pubs := p.Publications()
	assert.Empty(t, pubs)
}

func TestProduct_UpdateDetails(t *testing.T) {
	now := time.Now().UTC()
	p, err := catalog.NewProduct("p1", "t1", "Original", "original", "desc", "c1", "Brand", "SS26", catalog.NewAttributes(nil), now)
	require.NoError(t, err)

	// Invalid empty name
	err = p.UpdateDetails("", "desc", "c1", "Brand", "SS26", catalog.NewAttributes(nil), now)
	assert.ErrorIs(t, err, catalog.ErrEmptyProductName)

	// Invalid empty category
	err = p.UpdateDetails("New Name", "desc", "", "Brand", "SS26", catalog.NewAttributes(nil), now)
	assert.ErrorIs(t, err, catalog.ErrEmptyCategoryIDOnProd)

	// Valid update
	err = p.UpdateDetails("Updated Name", "Updated desc", "c2", "NewBrand", "FW26", catalog.NewAttributes(map[string]any{"origin": "Italy"}), now)
	require.NoError(t, err)
	assert.Equal(t, "Updated Name", p.Name())
	assert.Equal(t, "Updated desc", p.Description())
	assert.Equal(t, "c2", p.CategoryID())
	assert.Equal(t, "NewBrand", p.Brand())
	assert.Equal(t, "FW26", p.Season())
	orig, ok := p.Attributes().GetString("origin")
	assert.True(t, ok)
	assert.Equal(t, "Italy", orig)
}

func TestReconstituteProduct(t *testing.T) {
	now := time.Now().UTC()
	v := makeTestVariant(t, "v1", "p1", "SKU-1", 5000)
	pub, err := catalog.NewTenantPublication("t1", catalog.PublicationStatusPublished, &now, now)
	require.NoError(t, err)

	p := catalog.ReconstituteProduct("p1", "t1", "Name", "slug", "desc", "c1", "Brand", "FW26", false, []catalog.ProductVariant{v}, []catalog.TenantPublication{pub}, catalog.NewAttributes(nil), now, now)

	assert.Equal(t, "p1", p.ID())
	assert.Len(t, p.Variants(), 1)
	assert.True(t, p.IsPublishedForTenant("t1"))
	assert.Equal(t, now, p.CreatedAt())
	assert.Equal(t, now, p.UpdatedAt())
	assert.Empty(t, p.Events())
}

func TestProduct_ZeroTimeFallback(t *testing.T) {
	p, err := catalog.NewProduct("p1", "t1", "Name", "name", "desc", "c1", "b", "s", catalog.NewAttributes(nil), time.Time{})
	require.NoError(t, err)
	assert.False(t, p.CreatedAt().IsZero())
	assert.False(t, p.UpdatedAt().IsZero())

	v := makeTestVariant(t, "v1", "p1", "SKU-Z", 100)
	err = p.AddVariant(v, time.Time{})
	require.NoError(t, err)

	err = p.UpdateVariant(v, time.Time{})
	require.NoError(t, err)

	err = p.PublishForTenant("t1", time.Time{})
	require.NoError(t, err)

	err = p.UnpublishForTenant("t1", time.Time{})
	require.NoError(t, err)

	err = p.RemoveVariant("v1", time.Time{})
	require.NoError(t, err)

	err = p.UpdateDetails("New", "desc", "c1", "b", "s", catalog.NewAttributes(nil), time.Time{})
	require.NoError(t, err)

	err = p.Archive(time.Time{})
	require.NoError(t, err)
}
