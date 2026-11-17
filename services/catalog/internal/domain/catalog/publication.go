package catalog

import (
	"strings"
	"time"
)

// PublicationStatus represents the tenant storefront visibility lifecycle state.
type PublicationStatus string

const (
	PublicationStatusDraft       PublicationStatus = "DRAFT"
	PublicationStatusPublished   PublicationStatus = "PUBLISHED"
	PublicationStatusUnpublished PublicationStatus = "UNPUBLISHED"
)

// TenantPublication represents the publication state of a product within a specific tenant storefront.
type TenantPublication struct {
	tenantID    string
	status      PublicationStatus
	publishedAt *time.Time
	updatedAt   time.Time
}

// NewTenantPublication constructs a validated TenantPublication value object.
func NewTenantPublication(tenantID string, status PublicationStatus, publishedAt *time.Time, updatedAt time.Time) (TenantPublication, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return TenantPublication{}, ErrEmptyTenantID
	}

	switch status {
	case PublicationStatusDraft, PublicationStatusPublished, PublicationStatusUnpublished:
	default:
		status = PublicationStatusDraft
	}

	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	return TenantPublication{
		tenantID:    tenantID,
		status:      status,
		publishedAt: publishedAt,
		updatedAt:   updatedAt,
	}, nil
}

// TenantID returns the tenant identifier.
func (tp TenantPublication) TenantID() string {
	return tp.tenantID
}

// Status returns the publication status.
func (tp TenantPublication) Status() PublicationStatus {
	return tp.status
}

// PublishedAt returns the initial publication timestamp, or nil if draft/unpublished.
func (tp TenantPublication) PublishedAt() *time.Time {
	return tp.publishedAt
}

// UpdatedAt returns the timestamp of the last publication status change.
func (tp TenantPublication) UpdatedAt() time.Time {
	return tp.updatedAt
}

// IsPublished returns true if the status is PUBLISHED.
func (tp TenantPublication) IsPublished() bool {
	return tp.status == PublicationStatusPublished
}
