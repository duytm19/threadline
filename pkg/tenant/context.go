package tenant

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	// ErrMissingTenant is returned when tenant context is absent from context.Context (fail-closed per AD-4).
	ErrMissingTenant = errors.New("tenant: missing tenant context in request")

	// ErrEmptyTenantID is returned when tenant ID is empty or whitespace.
	ErrEmptyTenantID = errors.New("tenant: tenant ID cannot be empty (fail-closed)")

	// ErrInvalidTenantID is returned when tenant ID format does not match allowed syntax.
	ErrInvalidTenantID = errors.New("tenant: tenant ID must be 2-64 characters matching [a-zA-Z0-9_-]")
)

// Allowed tenant ID syntax: 2 to 64 alphanumeric characters, underscores, or hyphens.
var tenantIDRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{1,63}$`)

// TenantContext defines the application port interface for inspecting and carrying tenant scope.
type TenantContext interface {
	// TenantID returns the unique tenant identifier.
	TenantID() string
	// String returns the tenant identifier formatted as string.
	String() string
}

type tenantContextImpl struct {
	tenantID string
}

func (t tenantContextImpl) TenantID() string {
	return t.tenantID
}

func (t tenantContextImpl) String() string {
	return t.tenantID
}

// New validates and creates a new TenantContext instance.
func New(tenantID string) (TenantContext, error) {
	trimmed := strings.TrimSpace(tenantID)
	if trimmed == "" {
		return nil, ErrEmptyTenantID
	}
	if !tenantIDRegex.MatchString(trimmed) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidTenantID, tenantID)
	}
	return tenantContextImpl{tenantID: trimmed}, nil
}

// MustNew creates a new TenantContext instance or panics if invalid.
func MustNew(tenantID string) TenantContext {
	tc, err := New(tenantID)
	if err != nil {
		panic(err)
	}
	return tc
}

type contextKey struct{}

var tenantKey = contextKey{}

// WithContext stores a TenantContext in Go stdlib context.Context.
func WithContext(ctx context.Context, tc TenantContext) context.Context {
	if tc == nil {
		return ctx
	}
	return context.WithValue(ctx, tenantKey, tc)
}

// WithTenantID validates tenantID and stores it as TenantContext in context.Context.
func WithTenantID(ctx context.Context, tenantID string) (context.Context, error) {
	tc, err := New(tenantID)
	if err != nil {
		return ctx, err
	}
	return WithContext(ctx, tc), nil
}

// FromContext extracts the TenantContext from context.Context.
// Returns ErrMissingTenant if absent or nil (fail-closed per AD-4).
func FromContext(ctx context.Context) (TenantContext, error) {
	if ctx == nil {
		return nil, ErrMissingTenant
	}
	val := ctx.Value(tenantKey)
	if val == nil {
		return nil, ErrMissingTenant
	}
	tc, ok := val.(TenantContext)
	if !ok || tc == nil || tc.TenantID() == "" {
		return nil, ErrMissingTenant
	}
	return tc, nil
}

// TenantIDFromContext extracts the raw tenant ID string from context.Context.
// Returns ErrMissingTenant if absent (fail-closed per AD-4).
func TenantIDFromContext(ctx context.Context) (string, error) {
	tc, err := FromContext(ctx)
	if err != nil {
		return "", err
	}
	return tc.TenantID(), nil
}

// MustTenantIDFromContext extracts tenant ID from context or panics if unset.
func MustTenantIDFromContext(ctx context.Context) string {
	id, err := TenantIDFromContext(ctx)
	if err != nil {
		panic(err)
	}
	return id
}

// Carrier represents an abstract key-value carrier for cross-network tenant propagation (HTTP/gRPC/Kafka).
type Carrier interface {
	Get(key string) string
	Set(key string, val string)
}

// HeaderTenantID is the canonical HTTP header and metadata key for tenant propagation.
const HeaderTenantID = "X-Tenant-ID"

// Inject injects the tenant ID from context into a carrier.
func Inject(ctx context.Context, carrier Carrier) error {
	id, err := TenantIDFromContext(ctx)
	if err != nil {
		return err
	}
	carrier.Set(HeaderTenantID, id)
	return nil
}

// Extract extracts tenant ID from a carrier, validates it, and injects into context.Context.
func Extract(ctx context.Context, carrier Carrier) (context.Context, error) {
	id := carrier.Get(HeaderTenantID)
	if id == "" {
		return ctx, ErrMissingTenant
	}
	return WithTenantID(ctx, id)
}
