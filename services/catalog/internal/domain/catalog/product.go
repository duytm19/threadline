package catalog

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

var productSlugRegex = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)

// Product is the Aggregate Root representing a multi-tenant catalog item with variants.
type Product struct {
	id           string
	tenantID     string
	name         string
	slug         string
	description  string
	categoryID   string
	brand        string
	season       string
	isArchived   bool
	variants     map[string]ProductVariant
	publications map[string]TenantPublication
	attributes   Attributes
	createdAt    time.Time
	updatedAt    time.Time

	events []DomainEvent
}

// NewProduct creates a new Product aggregate root enforcing domain invariants.
func NewProduct(
	id string,
	tenantID string,
	name string,
	slug string,
	description string,
	categoryID string,
	brand string,
	season string,
	attrs Attributes,
	now time.Time,
) (*Product, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrEmptyProductID
	}

	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil, ErrEmptyTenantID
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrEmptyProductName
	}

	slug = strings.ToLower(strings.TrimSpace(slug))
	if !productSlugRegex.MatchString(slug) || len(slug) < 2 || len(slug) > 128 {
		return nil, ErrInvalidProductSlug
	}

	categoryID = strings.TrimSpace(categoryID)
	if categoryID == "" {
		return nil, ErrEmptyCategoryIDOnProd
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	p := &Product{
		id:           id,
		tenantID:     tenantID,
		name:         name,
		slug:         slug,
		description:  strings.TrimSpace(description),
		categoryID:   categoryID,
		brand:        strings.TrimSpace(brand),
		season:       strings.TrimSpace(season),
		isArchived:   false,
		variants:     make(map[string]ProductVariant),
		publications: make(map[string]TenantPublication),
		attributes:   attrs,
		createdAt:    now,
		updatedAt:    now,
	}

	p.recordEvent(ProductCreatedEvent{
		ProductID:  id,
		TenantID:   tenantID,
		Name:       name,
		CategoryID: categoryID,
		Occurred:   now,
	})

	return p, nil
}

// ReconstituteProduct rebuilds a Product aggregate root from persistence data.
func ReconstituteProduct(
	id string,
	tenantID string,
	name string,
	slug string,
	description string,
	categoryID string,
	brand string,
	season string,
	isArchived bool,
	variants []ProductVariant,
	publications []TenantPublication,
	attrs Attributes,
	createdAt time.Time,
	updatedAt time.Time,
) *Product {
	variantMap := make(map[string]ProductVariant, len(variants))
	for _, v := range variants {
		variantMap[v.ID()] = v
	}

	pubMap := make(map[string]TenantPublication, len(publications))
	for _, pub := range publications {
		pubMap[pub.TenantID()] = pub
	}

	return &Product{
		id:           id,
		tenantID:     tenantID,
		name:         name,
		slug:         slug,
		description:  description,
		categoryID:   categoryID,
		brand:        brand,
		season:       season,
		isArchived:   isArchived,
		variants:     variantMap,
		publications: pubMap,
		attributes:   attrs,
		createdAt:    createdAt,
		updatedAt:    updatedAt,
	}
}

// AddVariant attaches a new variant to the product, enforcing SKU uniqueness.
func (p *Product) AddVariant(v ProductVariant, now time.Time) error {
	if p.isArchived {
		return ErrProductArchived
	}

	// Verify SKU uniqueness across all existing variants in product.
	for _, existing := range p.variants {
		if existing.SKU().Value() == v.SKU().Value() {
			return ErrDuplicateSKU
		}
		if existing.ID() == v.ID() {
			return ErrDuplicateSKU
		}
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	p.variants[v.ID()] = v
	p.updatedAt = now

	p.recordEvent(VariantAddedEvent{
		ProductID: p.id,
		VariantID: v.ID(),
		SKU:       v.SKU().Value(),
		Occurred:  now,
	})

	return nil
}

// UpdateVariant modifies an existing variant, validating SKU uniqueness across others.
func (p *Product) UpdateVariant(v ProductVariant, now time.Time) error {
	if p.isArchived {
		return ErrProductArchived
	}

	if _, exists := p.variants[v.ID()]; !exists {
		return ErrVariantNotFound
	}

	// Validate SKU collision against any other variant.
	for _, existing := range p.variants {
		if existing.ID() != v.ID() && existing.SKU().Value() == v.SKU().Value() {
			return ErrDuplicateSKU
		}
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	p.variants[v.ID()] = v
	p.updatedAt = now

	p.recordEvent(VariantUpdatedEvent{
		ProductID: p.id,
		VariantID: v.ID(),
		SKU:       v.SKU().Value(),
		Occurred:  now,
	})

	return nil
}

// RemoveVariant removes a variant, ensuring a published product retains at least 1 active variant.
func (p *Product) RemoveVariant(variantID string, now time.Time) error {
	if p.isArchived {
		return ErrProductArchived
	}

	target, exists := p.variants[variantID]
	if !exists {
		return ErrVariantNotFound
	}

	if p.isPublishedAnywhere() && target.IsActive() && p.activeVariantCount() <= 1 {
		return ErrCannotRemoveLastVariant
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	delete(p.variants, variantID)
	p.updatedAt = now

	p.recordEvent(VariantRemovedEvent{
		ProductID: p.id,
		VariantID: target.ID(),
		SKU:       target.SKU().Value(),
		Occurred:  now,
	})

	return nil
}

// PublishForTenant publishes the product for a specific tenant storefront.
func (p *Product) PublishForTenant(tenantID string, now time.Time) error {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return ErrEmptyTenantID
	}

	if p.isArchived {
		return ErrProductArchived
	}

	if p.activeVariantCount() == 0 {
		return ErrProductNoVariants
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	pubTime := now
	existing, exists := p.publications[tenantID]
	if exists && existing.PublishedAt() != nil {
		pubTime = *existing.PublishedAt()
	}

	pub, _ := NewTenantPublication(tenantID, PublicationStatusPublished, &pubTime, now)
	p.publications[tenantID] = pub
	p.updatedAt = now

	p.recordEvent(ProductPublishedEvent{
		ProductID: p.id,
		TenantID:   tenantID,
		Occurred:   now,
	})

	return nil
}

// UnpublishForTenant unpublishes the product from a specific tenant storefront.
func (p *Product) UnpublishForTenant(tenantID string, now time.Time) error {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return ErrEmptyTenantID
	}

	if p.isArchived {
		return ErrProductArchived
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	var pubTime *time.Time
	if existing, exists := p.publications[tenantID]; exists {
		pubTime = existing.PublishedAt()
	}

	pub, _ := NewTenantPublication(tenantID, PublicationStatusUnpublished, pubTime, now)
	p.publications[tenantID] = pub
	p.updatedAt = now

	p.recordEvent(ProductUnpublishedEvent{
		ProductID: p.id,
		TenantID:   tenantID,
		Occurred:   now,
	})

	return nil
}

// IsPublishedForTenant checks if the product is live on a specific tenant storefront.
func (p *Product) IsPublishedForTenant(tenantID string) bool {
	if p.isArchived || tenantID == "" {
		return false
	}
	pub, exists := p.publications[tenantID]
	return exists && pub.IsPublished()
}

// Archive irreversibly deactivates the product and revokes all tenant publications.
func (p *Product) Archive(now time.Time) error {
	if p.isArchived {
		return nil
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	p.isArchived = true
	p.updatedAt = now

	// Unpublish from all active tenant storefronts.
	for tID, pub := range p.publications {
		p.publications[tID], _ = NewTenantPublication(tID, PublicationStatusUnpublished, pub.PublishedAt(), now)
	}

	p.recordEvent(ProductArchivedEvent{
		ProductID: p.id,
		TenantID:   p.tenantID,
		Occurred:   now,
	})

	return nil
}

// UpdateDetails updates common product metadata.
func (p *Product) UpdateDetails(name, description, categoryID, brand, season string, attrs Attributes, now time.Time) error {
	if p.isArchived {
		return ErrProductArchived
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return ErrEmptyProductName
	}

	categoryID = strings.TrimSpace(categoryID)
	if categoryID == "" {
		return ErrEmptyCategoryIDOnProd
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	p.name = name
	p.description = strings.TrimSpace(description)
	p.categoryID = categoryID
	p.brand = strings.TrimSpace(brand)
	p.season = strings.TrimSpace(season)
	p.attributes = attrs
	p.updatedAt = now

	return nil
}

// Variant queries

// VariantByID returns a pointer to the variant with matching ID.
func (p *Product) VariantByID(id string) (*ProductVariant, error) {
	v, exists := p.variants[id]
	if !exists {
		return nil, ErrVariantNotFound
	}
	return &v, nil
}

// VariantBySKU returns a pointer to the variant matching SKU string.
func (p *Product) VariantBySKU(sku SKU) (*ProductVariant, error) {
	for _, v := range p.variants {
		if v.SKU().Value() == sku.Value() {
			return &v, nil
		}
	}
	return nil, ErrVariantNotFound
}

// Variants returns all variants sorted by SKU.
func (p *Product) Variants() []ProductVariant {
	res := make([]ProductVariant, 0, len(p.variants))
	for _, v := range p.variants {
		res = append(res, v)
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].SKU().Value() < res[j].SKU().Value()
	})
	return res
}

// ActiveVariants returns all active variants sorted by SKU.
func (p *Product) ActiveVariants() []ProductVariant {
	res := make([]ProductVariant, 0, len(p.variants))
	for _, v := range p.variants {
		if v.IsActive() {
			res = append(res, v)
		}
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].SKU().Value() < res[j].SKU().Value()
	})
	return res
}

// Publications returns a defensive copy of all tenant publication records.
func (p *Product) Publications() map[string]TenantPublication {
	res := make(map[string]TenantPublication, len(p.publications))
	for k, v := range p.publications {
		res[k] = v
	}
	return res
}

// PublicationForTenant returns the publication record for a specific tenant.
func (p *Product) PublicationForTenant(tenantID string) (TenantPublication, bool) {
	pub, exists := p.publications[tenantID]
	return pub, exists
}

// Getters

func (p *Product) ID() string             { return p.id }
func (p *Product) TenantID() string       { return p.tenantID }
func (p *Product) Name() string           { return p.name }
func (p *Product) Slug() string           { return p.slug }
func (p *Product) Description() string    { return p.description }
func (p *Product) CategoryID() string     { return p.categoryID }
func (p *Product) Brand() string          { return p.brand }
func (p *Product) Season() string         { return p.season }
func (p *Product) IsArchived() bool       { return p.isArchived }
func (p *Product) Attributes() Attributes { return p.attributes }
func (p *Product) CreatedAt() time.Time   { return p.createdAt }
func (p *Product) UpdatedAt() time.Time   { return p.updatedAt }

// Event handling

func (p *Product) recordEvent(evt DomainEvent) {
	p.events = append(p.events, evt)
}

func (p *Product) Events() []DomainEvent {
	copied := make([]DomainEvent, len(p.events))
	copy(copied, p.events)
	return copied
}

func (p *Product) PopEvents() []DomainEvent {
	events := p.events
	p.events = nil
	return events
}

// Internal helpers

func (p *Product) activeVariantCount() int {
	count := 0
	for _, v := range p.variants {
		if v.IsActive() {
			count++
		}
	}
	return count
}

func (p *Product) isPublishedAnywhere() bool {
	for _, pub := range p.publications {
		if pub.IsPublished() {
			return true
		}
	}
	return false
}
