package catalog_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/threadline/services/catalog/internal/domain/catalog"
)

func TestDomainEvents_Contract(t *testing.T) {
	now := time.Now().UTC()
	parentID := "p-1"

	events := []catalog.DomainEvent{
		catalog.ProductCreatedEvent{ProductID: "p1", TenantID: "t1", Name: "N", CategoryID: "c1", Occurred: now},
		catalog.ProductPublishedEvent{ProductID: "p1", TenantID: "t1", Occurred: now},
		catalog.ProductUnpublishedEvent{ProductID: "p1", TenantID: "t1", Occurred: now},
		catalog.ProductArchivedEvent{ProductID: "p1", TenantID: "t1", Occurred: now},
		catalog.VariantAddedEvent{ProductID: "p1", VariantID: "v1", SKU: "SKU1", Occurred: now},
		catalog.VariantUpdatedEvent{ProductID: "p1", VariantID: "v1", SKU: "SKU1", Occurred: now},
		catalog.VariantRemovedEvent{ProductID: "p1", VariantID: "v1", SKU: "SKU1", Occurred: now},
		catalog.CategoryCreatedEvent{CategoryID: "c1", TenantID: "t1", Name: "N", Slug: "s", Path: "s", ParentID: &parentID, Occurred: now},
		catalog.CategoryMovedEvent{CategoryID: "c1", TenantID: "t1", OldPath: "old", NewPath: "new", ParentID: &parentID, Occurred: now},
		catalog.CategoryActivatedEvent{CategoryID: "c1", TenantID: "t1", Occurred: now},
		catalog.CategoryDeactivatedEvent{CategoryID: "c1", TenantID: "t1", Occurred: now},
	}

	for _, evt := range events {
		assert.NotEmpty(t, evt.EventName())
		assert.Equal(t, now, evt.OccurredAt())
	}
}
