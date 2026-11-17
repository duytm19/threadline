package catalog

import "time"

// DomainEvent is the interface implemented by all catalog domain events.
type DomainEvent interface {
	EventName() string
	OccurredAt() time.Time
}

// ProductCreatedEvent is emitted when a new product aggregate is created.
type ProductCreatedEvent struct {
	ProductID  string
	TenantID   string
	Name       string
	CategoryID string
	Occurred   time.Time
}

func (e ProductCreatedEvent) EventName() string     { return "catalog.product.created" }
func (e ProductCreatedEvent) OccurredAt() time.Time { return e.Occurred }

// ProductPublishedEvent is emitted when a product is published for a tenant.
type ProductPublishedEvent struct {
	ProductID string
	TenantID  string
	Occurred  time.Time
}

func (e ProductPublishedEvent) EventName() string     { return "catalog.product.published" }
func (e ProductPublishedEvent) OccurredAt() time.Time { return e.Occurred }

// ProductUnpublishedEvent is emitted when a product is unpublished for a tenant.
type ProductUnpublishedEvent struct {
	ProductID string
	TenantID  string
	Occurred  time.Time
}

func (e ProductUnpublishedEvent) EventName() string     { return "catalog.product.unpublished" }
func (e ProductUnpublishedEvent) OccurredAt() time.Time { return e.Occurred }

// ProductArchivedEvent is emitted when a product is permanently archived.
type ProductArchivedEvent struct {
	ProductID string
	TenantID  string
	Occurred  time.Time
}

func (e ProductArchivedEvent) EventName() string     { return "catalog.product.archived" }
func (e ProductArchivedEvent) OccurredAt() time.Time { return e.Occurred }

// VariantAddedEvent is emitted when a new SKU variant is attached to a product.
type VariantAddedEvent struct {
	ProductID string
	VariantID string
	SKU       string
	Occurred  time.Time
}

func (e VariantAddedEvent) EventName() string     { return "catalog.variant.added" }
func (e VariantAddedEvent) OccurredAt() time.Time { return e.Occurred }

// VariantUpdatedEvent is emitted when variant details or pricing change.
type VariantUpdatedEvent struct {
	ProductID string
	VariantID string
	SKU       string
	Occurred  time.Time
}

func (e VariantUpdatedEvent) EventName() string     { return "catalog.variant.updated" }
func (e VariantUpdatedEvent) OccurredAt() time.Time { return e.Occurred }

// VariantRemovedEvent is emitted when a variant is removed from a product.
type VariantRemovedEvent struct {
	ProductID string
	VariantID string
	SKU       string
	Occurred  time.Time
}

func (e VariantRemovedEvent) EventName() string     { return "catalog.variant.removed" }
func (e VariantRemovedEvent) OccurredAt() time.Time { return e.Occurred }

// CategoryCreatedEvent is emitted when a new category is defined.
type CategoryCreatedEvent struct {
	CategoryID string
	TenantID   string
	Name       string
	Slug       string
	Path       string
	ParentID   *string
	Occurred   time.Time
}

func (e CategoryCreatedEvent) EventName() string     { return "catalog.category.created" }
func (e CategoryCreatedEvent) OccurredAt() time.Time { return e.Occurred }

// CategoryMovedEvent is emitted when a category is reparented or path changes.
type CategoryMovedEvent struct {
	CategoryID string
	TenantID   string
	OldPath    string
	NewPath    string
	ParentID   *string
	Occurred   time.Time
}

func (e CategoryMovedEvent) EventName() string     { return "catalog.category.moved" }
func (e CategoryMovedEvent) OccurredAt() time.Time { return e.Occurred }

// CategoryActivatedEvent is emitted when category is enabled for browsing.
type CategoryActivatedEvent struct {
	CategoryID string
	TenantID   string
	Occurred   time.Time
}

func (e CategoryActivatedEvent) EventName() string     { return "catalog.category.activated" }
func (e CategoryActivatedEvent) OccurredAt() time.Time { return e.Occurred }

// CategoryDeactivatedEvent is emitted when category is disabled.
type CategoryDeactivatedEvent struct {
	CategoryID string
	TenantID   string
	Occurred   time.Time
}

func (e CategoryDeactivatedEvent) EventName() string     { return "catalog.category.deactivated" }
func (e CategoryDeactivatedEvent) OccurredAt() time.Time { return e.Occurred }
