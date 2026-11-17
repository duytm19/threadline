package catalog

import "errors"

var (
	// Category domain errors
	ErrEmptyCategoryID       = errors.New("catalog: category id cannot be empty")
	ErrEmptyCategoryName     = errors.New("catalog: category name cannot be empty")
	ErrInvalidCategorySlug   = errors.New("catalog: invalid category slug")
	ErrCategorySelfParent    = errors.New("catalog: category cannot be its own parent")
	ErrCategoryCycleDetected = errors.New("catalog: category cannot be moved under its own descendant")
	ErrCategoryDepthExceeded = errors.New("catalog: category hierarchy exceeds maximum allowed depth")

	// Product domain errors
	ErrEmptyProductID        = errors.New("catalog: product id cannot be empty")
	ErrEmptyProductName      = errors.New("catalog: product name cannot be empty")
	ErrInvalidProductSlug    = errors.New("catalog: invalid product slug")
	ErrEmptyCategoryIDOnProd = errors.New("catalog: product must be assigned to a valid category")
	ErrProductNoVariants     = errors.New("catalog: product must have at least one active variant to be published")
	ErrProductArchived       = errors.New("catalog: archived product cannot be modified or published")
	ErrProductNotPublished   = errors.New("catalog: product is not published for the requested tenant")

	// Variant domain errors
	ErrEmptyVariantID          = errors.New("catalog: variant id cannot be empty")
	ErrEmptySKU                = errors.New("catalog: SKU cannot be empty")
	ErrInvalidSKU              = errors.New("catalog: invalid SKU format")
	ErrDuplicateSKU            = errors.New("catalog: SKU already exists in product")
	ErrVariantNotFound         = errors.New("catalog: variant not found in product")
	ErrCannotRemoveLastVariant = errors.New("catalog: cannot remove the only active variant from a published product")
	ErrNegativeWeight          = errors.New("catalog: weight cannot be negative")

	// Money & Price domain errors
	ErrNegativeAmount   = errors.New("catalog: price amount cannot be negative")
	ErrInvalidCurrency  = errors.New("catalog: currency must be a 3-letter uppercase ISO code")
	ErrCurrencyMismatch = errors.New("catalog: cannot operate on different currencies")

	// Multi-tenant isolation domain errors
	ErrEmptyTenantID = errors.New("catalog: tenant id cannot be empty (fail-closed)")
)
