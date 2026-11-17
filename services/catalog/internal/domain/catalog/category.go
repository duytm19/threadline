package catalog

import (
	"regexp"
	"strings"
	"time"
)

// MaxCategoryDepth defines the maximum nesting depth for catalog taxonomies.
const MaxCategoryDepth = 6

var slugRegex = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)

// Category represents a taxonomy node in the product catalog hierarchy.
type Category struct {
	id          string
	tenantID    string
	name        string
	slug        string
	description string
	parentID    *string
	path        string
	level       int
	isActive    bool
	createdAt   time.Time
	updatedAt   time.Time

	events []DomainEvent
}

// NewCategory creates a new Category aggregate root enforcing taxonomy invariants.
func NewCategory(
	id string,
	tenantID string,
	name string,
	slug string,
	description string,
	parent *Category,
	now time.Time,
) (*Category, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrEmptyCategoryID
	}

	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return nil, ErrEmptyTenantID
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrEmptyCategoryName
	}

	slug = strings.ToLower(strings.TrimSpace(slug))
	if !slugRegex.MatchString(slug) || len(slug) < 2 || len(slug) > 64 {
		return nil, ErrInvalidCategorySlug
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	pathSegment := toPathSegment(slug)

	var parentID *string
	var path string
	var level int

	if parent == nil {
		path = pathSegment
		level = 1
	} else {
		if parent.tenantID != tenantID {
			return nil, ErrEmptyTenantID
		}
		if parent.level >= MaxCategoryDepth {
			return nil, ErrCategoryDepthExceeded
		}
		pID := parent.id
		parentID = &pID
		path = parent.path + "." + pathSegment
		level = parent.level + 1
	}

	c := &Category{
		id:          id,
		tenantID:    tenantID,
		name:        name,
		slug:        slug,
		description: strings.TrimSpace(description),
		parentID:    parentID,
		path:        path,
		level:       level,
		isActive:    true,
		createdAt:     now,
		updatedAt:   now,
	}

	c.recordEvent(CategoryCreatedEvent{
		CategoryID: id,
		TenantID:   tenantID,
		Name:       name,
		Slug:       slug,
		Path:       path,
		ParentID:   parentID,
		Occurred:   now,
	})

	return c, nil
}

// ReconstituteCategory builds a Category from repository data without generating events.
func ReconstituteCategory(
	id string,
	tenantID string,
	name string,
	slug string,
	description string,
	parentID *string,
	path string,
	level int,
	isActive bool,
	createdAt time.Time,
	updatedAt time.Time,
) *Category {
	return &Category{
		id:          id,
		tenantID:    tenantID,
		name:        name,
		slug:        slug,
		description: description,
		parentID:    parentID,
		path:        path,
		level:       level,
		isActive:    isActive,
		createdAt:     createdAt,
		updatedAt:   updatedAt,
	}
}

// Move changes the parent of the category, enforcing acyclic graph and depth limits.
func (c *Category) Move(newParent *Category, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}

	oldPath := c.path
	pathSegment := toPathSegment(c.slug)

	if newParent == nil {
		c.parentID = nil
		c.path = pathSegment
		c.level = 1
	} else {
		if newParent.id == c.id {
			return ErrCategorySelfParent
		}
		if newParent.tenantID != c.tenantID {
			return ErrEmptyTenantID
		}
		// Cycle check: cannot move under self or any of its own descendants.
		if newParent.path == c.path || strings.HasPrefix(newParent.path, c.path+".") {
			return ErrCategoryCycleDetected
		}
		if newParent.level+1 > MaxCategoryDepth {
			return ErrCategoryDepthExceeded
		}

		pID := newParent.id
		c.parentID = &pID
		c.path = newParent.path + "." + pathSegment
		c.level = newParent.level + 1
	}

	c.updatedAt = now
	c.recordEvent(CategoryMovedEvent{
		CategoryID: c.id,
		TenantID:   c.tenantID,
		OldPath:    oldPath,
		NewPath:    c.path,
		ParentID:   c.parentID,
		Occurred:   now,
	})

	return nil
}

// Update modifies display name and description.
func (c *Category) Update(name, description string, now time.Time) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrEmptyCategoryName
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	c.name = name
	c.description = strings.TrimSpace(description)
	c.updatedAt = now
	return nil
}

// Activate marks the category active for storefront browsing.
func (c *Category) Activate(now time.Time) {
	if c.isActive {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	c.isActive = true
	c.updatedAt = now
	c.recordEvent(CategoryActivatedEvent{
		CategoryID: c.id,
		TenantID:   c.tenantID,
		Occurred:   now,
	})
}

// Deactivate hides the category from storefront browsing.
func (c *Category) Deactivate(now time.Time) {
	if !c.isActive {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	c.isActive = false
	c.updatedAt = now
	c.recordEvent(CategoryDeactivatedEvent{
		CategoryID: c.id,
		TenantID:   c.tenantID,
		Occurred:   now,
	})
}

// Getters

func (c *Category) ID() string           { return c.id }
func (c *Category) TenantID() string     { return c.tenantID }
func (c *Category) Name() string         { return c.name }
func (c *Category) Slug() string         { return c.slug }
func (c *Category) Description() string  { return c.description }
func (c *Category) ParentID() *string    { return c.parentID }
func (c *Category) Path() string         { return c.path }
func (c *Category) Level() int           { return c.level }
func (c *Category) IsActive() bool       { return c.isActive }
func (c *Category) CreatedAt() time.Time { return c.createdAt }
func (c *Category) UpdatedAt() time.Time { return c.updatedAt }

// Event handling

func (c *Category) recordEvent(evt DomainEvent) {
	c.events = append(c.events, evt)
}

func (c *Category) Events() []DomainEvent {
	copied := make([]DomainEvent, len(c.events))
	copy(copied, c.events)
	return copied
}

func (c *Category) PopEvents() []DomainEvent {
	events := c.events
	c.events = nil
	return events
}

// toPathSegment formats slug to PostgreSQL ltree-safe label.
func toPathSegment(slug string) string {
	return strings.ReplaceAll(slug, "-", "_")
}
