package catalog_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/threadline/services/catalog/internal/domain/catalog"
)

func TestTenantPublication(t *testing.T) {
	now := time.Now().UTC()

	// Empty tenant error
	_, err := catalog.NewTenantPublication("", catalog.PublicationStatusPublished, &now, now)
	assert.ErrorIs(t, err, catalog.ErrEmptyTenantID)

	// Default status fallback
	pubDefault, err := catalog.NewTenantPublication("t1", "UNKNOWN_STATUS", nil, now)
	require.NoError(t, err)
	assert.Equal(t, catalog.PublicationStatusDraft, pubDefault.Status())
	assert.False(t, pubDefault.IsPublished())
	assert.Nil(t, pubDefault.PublishedAt())

	// Published status
	pubPublished, err := catalog.NewTenantPublication("t1", catalog.PublicationStatusPublished, &now, time.Time{})
	require.NoError(t, err)
	assert.Equal(t, "t1", pubPublished.TenantID())
	assert.Equal(t, catalog.PublicationStatusPublished, pubPublished.Status())
	assert.True(t, pubPublished.IsPublished())
	assert.Equal(t, now, *pubPublished.PublishedAt())
	assert.False(t, pubPublished.UpdatedAt().IsZero())
}
