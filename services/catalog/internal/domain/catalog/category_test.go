package catalog_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/threadline/services/catalog/internal/domain/catalog"
)

func TestNewCategory_Success(t *testing.T) {
	now := time.Now().UTC()

	// Root category
	root, err := catalog.NewCategory("cat-1", "tenant-1", "Women's Fashion", "womens-fashion", "All women fashion items", nil, now)
	require.NoError(t, err)
	assert.Equal(t, "cat-1", root.ID())
	assert.Equal(t, "tenant-1", root.TenantID())
	assert.Equal(t, "Women's Fashion", root.Name())
	assert.Equal(t, "womens-fashion", root.Slug())
	assert.Equal(t, "womens_fashion", root.Path())
	assert.Equal(t, 1, root.Level())
	assert.Nil(t, root.ParentID())
	assert.True(t, root.IsActive())

	events := root.PopEvents()
	require.Len(t, events, 1)
	assert.Equal(t, "catalog.category.created", events[0].EventName())
	assert.Equal(t, now, events[0].OccurredAt())

	// Child category
	child, err := catalog.NewCategory("cat-2", "tenant-1", "Dresses", "dresses", "Women's dresses", root, now)
	require.NoError(t, err)
	assert.Equal(t, "cat-2", child.ID())
	assert.Equal(t, "womens_fashion.dresses", child.Path())
	assert.Equal(t, 2, child.Level())
	require.NotNil(t, child.ParentID())
	assert.Equal(t, "cat-1", *child.ParentID())
}

func TestNewCategory_ValidationErrors(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name        string
		id          string
		tenantID    string
		catName     string
		slug        string
		parent      *catalog.Category
		expectedErr error
	}{
		{"empty id", "", "tenant-1", "Name", "slug", nil, catalog.ErrEmptyCategoryID},
		{"empty tenant", "c1", "", "Name", "slug", nil, catalog.ErrEmptyTenantID},
		{"empty name", "c1", "tenant-1", "", "slug", nil, catalog.ErrEmptyCategoryName},
		{"empty slug", "c1", "tenant-1", "Name", "", nil, catalog.ErrInvalidCategorySlug},
		{"invalid slug uppercase or spaces", "c1", "tenant-1", "Name", "Invalid Slug", nil, catalog.ErrInvalidCategorySlug},
		{"slug too short", "c1", "tenant-1", "Name", "a", nil, catalog.ErrInvalidCategorySlug},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := catalog.NewCategory(tt.id, tt.tenantID, tt.catName, tt.slug, "desc", tt.parent, now)
			assert.ErrorIs(t, err, tt.expectedErr)
		})
	}
}

func TestCategory_TenantMismatch(t *testing.T) {
	now := time.Now().UTC()
	parent, err := catalog.NewCategory("cat-root", "tenant-1", "Root", "root", "", nil, now)
	require.NoError(t, err)

	_, err = catalog.NewCategory("cat-child", "tenant-2", "Child", "child", "", parent, now)
	assert.ErrorIs(t, err, catalog.ErrEmptyTenantID)
}

func TestCategory_MaxDepthExceeded(t *testing.T) {
	now := time.Now().UTC()
	var current *catalog.Category
	var err error

	// Create 6 levels (maximum allowed)
	for i := 1; i <= catalog.MaxCategoryDepth; i++ {
		current, err = catalog.NewCategory(
			"cat-"+string(rune('0'+i)),
			"tenant-1",
			"Level",
			"level-"+string(rune('0'+i)),
			"",
			current,
			now,
		)
		require.NoError(t, err)
		assert.Equal(t, i, current.Level())
	}

	// Level 7 must fail
	_, err = catalog.NewCategory("cat-7", "tenant-1", "Level 7", "level-7", "", current, now)
	assert.ErrorIs(t, err, catalog.ErrCategoryDepthExceeded)
}

func TestCategory_Move_AcyclicAndDepthInvariants(t *testing.T) {
	now := time.Now().UTC()
	root1, err := catalog.NewCategory("r1", "tenant-1", "Apparel", "apparel", "", nil, now)
	require.NoError(t, err)

	sub1, err := catalog.NewCategory("s1", "tenant-1", "Tops", "tops", "", root1, now)
	require.NoError(t, err)

	sub2, err := catalog.NewCategory("s2", "tenant-1", "T-Shirts", "t-shirts", "", sub1, now)
	require.NoError(t, err)

	root2, err := catalog.NewCategory("r2", "tenant-1", "Footwear", "footwear", "", nil, now)
	require.NoError(t, err)

	// Invariant 1: Cannot move under itself
	err = root1.Move(root1, now)
	assert.ErrorIs(t, err, catalog.ErrCategorySelfParent)

	// Invariant 2: Cannot move under descendant (cycle detection)
	err = root1.Move(sub2, now)
	assert.ErrorIs(t, err, catalog.ErrCategoryCycleDetected)

	err = root1.Move(sub1, now)
	assert.ErrorIs(t, err, catalog.ErrCategoryCycleDetected)

	// Move sub1 under root2 (valid reparenting)
	err = sub1.Move(root2, now)
	require.NoError(t, err)
	assert.Equal(t, "footwear.tops", sub1.Path())
	assert.Equal(t, 2, sub1.Level())
	require.NotNil(t, sub1.ParentID())
	assert.Equal(t, "r2", *sub1.ParentID())

	// Move sub1 to root
	err = sub1.Move(nil, now)
	require.NoError(t, err)
	assert.Equal(t, "tops", sub1.Path())
	assert.Equal(t, 1, sub1.Level())
	assert.Nil(t, sub1.ParentID())

	// Check move events
	events := sub1.PopEvents()
	var movedEvents []catalog.CategoryMovedEvent
	for _, e := range events {
		if me, ok := e.(catalog.CategoryMovedEvent); ok {
			movedEvents = append(movedEvents, me)
		}
	}
	assert.Len(t, movedEvents, 2)
}

func TestCategory_Activate_Deactivate_Update(t *testing.T) {
	now := time.Now().UTC()
	cat, err := catalog.NewCategory("c1", "t1", "Original", "original", "desc", nil, now)
	require.NoError(t, err)
	cat.PopEvents() // discard creation event

	// Update details
	err = cat.Update("", "desc", now)
	assert.ErrorIs(t, err, catalog.ErrEmptyCategoryName)

	err = cat.Update("Updated Name", "New description", now)
	require.NoError(t, err)
	assert.Equal(t, "Updated Name", cat.Name())
	assert.Equal(t, "New description", cat.Description())

	// Deactivate
	cat.Deactivate(now)
	assert.False(t, cat.IsActive())
	evts := cat.PopEvents()
	require.Len(t, evts, 1)
	assert.Equal(t, "catalog.category.deactivated", evts[0].EventName())

	// Deactivate again should be idempotent
	cat.Deactivate(now)
	assert.Empty(t, cat.PopEvents())

	// Activate
	cat.Activate(now)
	assert.True(t, cat.IsActive())
	evts = cat.PopEvents()
	require.Len(t, evts, 1)
	assert.Equal(t, "catalog.category.activated", evts[0].EventName())

	// Activate again should be idempotent
	cat.Activate(now)
	assert.Empty(t, cat.PopEvents())
}

func TestReconstituteCategory(t *testing.T) {
	now := time.Now().UTC()
	pID := "parent-1"
	cat := catalog.ReconstituteCategory("c1", "t1", "Name", "slug", "desc", &pID, "parent_1.slug", 2, true, now, now)

	assert.Equal(t, "c1", cat.ID())
	assert.Equal(t, "t1", cat.TenantID())
	assert.Equal(t, "Name", cat.Name())
	assert.Equal(t, "slug", cat.Slug())
	assert.Equal(t, "desc", cat.Description())
	assert.Equal(t, "parent-1", *cat.ParentID())
	assert.Equal(t, "parent_1.slug", cat.Path())
	assert.Equal(t, 2, cat.Level())
	assert.True(t, cat.IsActive())
	assert.Equal(t, now, cat.CreatedAt())
	assert.Equal(t, now, cat.UpdatedAt())
	assert.Empty(t, cat.Events())
}

func TestCategory_ZeroTimeFallback(t *testing.T) {
	cat, err := catalog.NewCategory("c1", "t1", "Name", "slug", "desc", nil, time.Time{})
	require.NoError(t, err)
	assert.False(t, cat.CreatedAt().IsZero())
	assert.False(t, cat.UpdatedAt().IsZero())

	err = cat.Update("New", "Desc", time.Time{})
	require.NoError(t, err)

	cat.Deactivate(time.Time{})
	assert.False(t, cat.IsActive())

	cat.Activate(time.Time{})
	assert.True(t, cat.IsActive())

	err = cat.Move(nil, time.Time{})
	require.NoError(t, err)
}
