package catalog

import (
	"strings"
	"time"
)

// ProductVariant represents a purchasable SKU variant with specific size, color, price, and dynamic attributes.
type ProductVariant struct {
	id          string
	productID   string
	sku         SKU
	name        string
	size        string
	color       string
	price       Money
	barcode     string
	weightGrams int
	attributes  Attributes
	isActive    bool
	createdAt   time.Time
	updatedAt   time.Time
}

// NewProductVariant constructs a new ProductVariant entity enforcing domain constraints.
func NewProductVariant(
	id string,
	productID string,
	sku SKU,
	name string,
	size string,
	color string,
	price Money,
	barcode string,
	weightGrams int,
	attrs Attributes,
	now time.Time,
) (*ProductVariant, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrEmptyVariantID
	}

	productID = strings.TrimSpace(productID)
	if productID == "" {
		return nil, ErrEmptyProductID
	}

	if sku.Value() == "" {
		return nil, ErrEmptySKU
	}

	if price.Currency() == "" {
		return nil, ErrInvalidCurrency
	}

	if weightGrams < 0 {
		return nil, ErrNegativeWeight
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	return &ProductVariant{
		id:          id,
		productID:   productID,
		sku:         sku,
		name:        strings.TrimSpace(name),
		size:        strings.TrimSpace(size),
		color:       strings.TrimSpace(color),
		price:       price,
		barcode:     strings.TrimSpace(barcode),
		weightGrams: weightGrams,
		attributes:  attrs,
		isActive:    true,
		createdAt:   now,
		updatedAt:   now,
	}, nil
}

// ReconstituteProductVariant rebuilds a ProductVariant from persistence.
func ReconstituteProductVariant(
	id string,
	productID string,
	sku SKU,
	name string,
	size string,
	color string,
	price Money,
	barcode string,
	weightGrams int,
	attrs Attributes,
	isActive bool,
	createdAt time.Time,
	updatedAt time.Time,
) *ProductVariant {
	return &ProductVariant{
		id:          id,
		productID:   productID,
		sku:         sku,
		name:        name,
		size:        size,
		color:       color,
		price:       price,
		barcode:     barcode,
		weightGrams: weightGrams,
		attributes:  attrs,
		isActive:    isActive,
		createdAt:   createdAt,
		updatedAt:   updatedAt,
	}
}

// UpdatePrice updates the monetary price of the variant.
func (v *ProductVariant) UpdatePrice(newPrice Money, now time.Time) error {
	if newPrice.Currency() == "" {
		return ErrInvalidCurrency
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	v.price = newPrice
	v.updatedAt = now
	return nil
}

// UpdateDetails updates the descriptive attributes and weight.
func (v *ProductVariant) UpdateDetails(name, size, color, barcode string, weightGrams int, attrs Attributes, now time.Time) error {
	if weightGrams < 0 {
		return ErrNegativeWeight
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	v.name = strings.TrimSpace(name)
	v.size = strings.TrimSpace(size)
	v.color = strings.TrimSpace(color)
	v.barcode = strings.TrimSpace(barcode)
	v.weightGrams = weightGrams
	v.attributes = attrs
	v.updatedAt = now
	return nil
}

// Activate enables the variant for purchasing.
func (v *ProductVariant) Activate(now time.Time) {
	if v.isActive {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	v.isActive = true
	v.updatedAt = now
}

// Deactivate disables the variant from purchasing.
func (v *ProductVariant) Deactivate(now time.Time) {
	if !v.isActive {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	v.isActive = false
	v.updatedAt = now
}

// Getters

func (v *ProductVariant) ID() string             { return v.id }
func (v *ProductVariant) ProductID() string      { return v.productID }
func (v *ProductVariant) SKU() SKU               { return v.sku }
func (v *ProductVariant) Name() string           { return v.name }
func (v *ProductVariant) Size() string           { return v.size }
func (v *ProductVariant) Color() string          { return v.color }
func (v *ProductVariant) Price() Money           { return v.price }
func (v *ProductVariant) Barcode() string        { return v.barcode }
func (v *ProductVariant) WeightGrams() int       { return v.weightGrams }
func (v *ProductVariant) Attributes() Attributes { return v.attributes }
func (v *ProductVariant) IsActive() bool         { return v.isActive }
func (v *ProductVariant) CreatedAt() time.Time   { return v.createdAt }
func (v *ProductVariant) UpdatedAt() time.Time   { return v.updatedAt }
