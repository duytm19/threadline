package tenant_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/threadline/pkg/tenant"
)

func TestNew_ValidTenantIDs(t *testing.T) {
	validIDs := []string{
		"zalora-vn",
		"iconic-au",
		"brand_us",
		"tenant123",
		"AB",
		"Tenant-01_Test",
	}

	for _, id := range validIDs {
		t.Run("Valid_"+id, func(t *testing.T) {
			tc, err := tenant.New(id)
			if err != nil {
				t.Fatalf("expected valid tenant ID, got err: %v", err)
			}
			if tc.TenantID() != id {
				t.Errorf("expected %s, got %s", id, tc.TenantID())
			}
			if tc.String() != id {
				t.Errorf("expected string %s, got %s", id, tc.String())
			}
		})
	}
}

func TestNew_InvalidTenantIDs(t *testing.T) {
	testCases := []struct {
		name        string
		input       string
		expectedErr error
	}{
		{name: "empty", input: "", expectedErr: tenant.ErrEmptyTenantID},
		{name: "whitespace", input: "   ", expectedErr: tenant.ErrEmptyTenantID},
		{name: "single_char", input: "a", expectedErr: tenant.ErrInvalidTenantID},
		{name: "special_chars", input: "zalora/vn", expectedErr: tenant.ErrInvalidTenantID},
		{name: "sql_injection", input: "'; DROP TABLE orders; --", expectedErr: tenant.ErrInvalidTenantID},
		{name: "leading_dash", input: "-zalora", expectedErr: tenant.ErrInvalidTenantID},
		{name: "too_long", input: strings.Repeat("a", 65), expectedErr: tenant.ErrInvalidTenantID},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tenant.New(tc.input)
			if err == nil {
				t.Fatalf("expected error for input %q, got nil", tc.input)
			}
			if !errors.Is(err, tc.expectedErr) {
				t.Errorf("expected %v, got %v", tc.expectedErr, err)
			}
		})
	}
}

func TestMustNew(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		tc := tenant.MustNew("zalora-vn")
		if tc.TenantID() != "zalora-vn" {
			t.Fatalf("unexpected tenant ID: %s", tc.TenantID())
		}
	})

	t.Run("panic on invalid", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("expected panic on invalid tenant ID")
			}
		}()
		tenant.MustNew("")
	})
}

func TestContextPropagation(t *testing.T) {
	ctx := context.Background()

	// 1. Fail-closed when context has no tenant
	t.Run("fail_closed_when_unset", func(t *testing.T) {
		_, err := tenant.FromContext(ctx)
		if !errors.Is(err, tenant.ErrMissingTenant) {
			t.Fatalf("expected ErrMissingTenant, got %v", err)
		}

		_, err = tenant.TenantIDFromContext(ctx)
		if !errors.Is(err, tenant.ErrMissingTenant) {
			t.Fatalf("expected ErrMissingTenant, got %v", err)
		}

		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("expected MustTenantIDFromContext to panic on unset context")
			}
		}()
		tenant.MustTenantIDFromContext(ctx)
	})

	// 2. Propagation with TenantContext
	t.Run("propagation_with_TenantContext", func(t *testing.T) {
		tc := tenant.MustNew("zalora-vn")
		tenantCtx := tenant.WithContext(ctx, tc)

		extracted, err := tenant.FromContext(tenantCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if extracted.TenantID() != "zalora-vn" {
			t.Errorf("expected zalora-vn, got %s", extracted.TenantID())
		}

		id, err := tenant.TenantIDFromContext(tenantCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "zalora-vn" {
			t.Errorf("expected zalora-vn, got %s", id)
		}

		mustID := tenant.MustTenantIDFromContext(tenantCtx)
		if mustID != "zalora-vn" {
			t.Errorf("expected zalora-vn, got %s", mustID)
		}
	})

	// 3. Propagation with WithTenantID helper
	t.Run("propagation_with_WithTenantID", func(t *testing.T) {
		tenantCtx, err := tenant.WithTenantID(ctx, "iconic-au")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		id, err := tenant.TenantIDFromContext(tenantCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != "iconic-au" {
			t.Errorf("expected iconic-au, got %s", id)
		}
	})

	// 4. WithTenantID returns error for invalid tenant
	t.Run("WithTenantID_rejects_invalid", func(t *testing.T) {
		_, err := tenant.WithTenantID(ctx, "invalid/tenant")
		if err == nil {
			t.Fatalf("expected error for invalid tenant, got nil")
		}
	})

	// 5. Nil context safety
	t.Run("nil_context_safety", func(t *testing.T) {
		_, err := tenant.FromContext(nil)
		if !errors.Is(err, tenant.ErrMissingTenant) {
			t.Fatalf("expected ErrMissingTenant, got %v", err)
		}
	})
}

type mockCarrier struct {
	data map[string]string
}

func (m *mockCarrier) Get(key string) string {
	return m.data[key]
}

func (m *mockCarrier) Set(key string, val string) {
	m.data[key] = val
}

func TestCarrier_InjectAndExtract(t *testing.T) {
	carrier := &mockCarrier{data: make(map[string]string)}

	// 1. Inject fails when context has no tenant
	ctx := context.Background()
	err := tenant.Inject(ctx, carrier)
	if !errors.Is(err, tenant.ErrMissingTenant) {
		t.Fatalf("expected ErrMissingTenant, got %v", err)
	}

	// 2. Inject succeeds when tenant present
	tenantCtx, _ := tenant.WithTenantID(ctx, "zalora-vn")
	if err := tenant.Inject(tenantCtx, carrier); err != nil {
		t.Fatalf("unexpected error injecting tenant: %v", err)
	}

	if carrier.Get(tenant.HeaderTenantID) != "zalora-vn" {
		t.Fatalf("carrier missing header: %v", carrier.data)
	}

	// 3. Extract succeeds from populated carrier
	extractedCtx, err := tenant.Extract(context.Background(), carrier)
	if err != nil {
		t.Fatalf("unexpected error extracting tenant: %v", err)
	}

	extractedID := tenant.MustTenantIDFromContext(extractedCtx)
	if extractedID != "zalora-vn" {
		t.Fatalf("expected zalora-vn, got %s", extractedID)
	}

	// 4. Extract fails when header missing in carrier
	emptyCarrier := &mockCarrier{data: make(map[string]string)}
	_, err = tenant.Extract(context.Background(), emptyCarrier)
	if !errors.Is(err, tenant.ErrMissingTenant) {
		t.Fatalf("expected ErrMissingTenant, got %v", err)
	}
}

type customTenantContext struct {
	customID string
}

func (c customTenantContext) TenantID() string {
	return c.customID
}

func (c customTenantContext) String() string {
	return c.customID
}

func TestCustomTenantContextInterface(t *testing.T) {
	custom := customTenantContext{customID: "custom-tenant-99"}
	ctx := tenant.WithContext(context.Background(), custom)

	tc, err := tenant.FromContext(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tc.TenantID() != "custom-tenant-99" {
		t.Errorf("expected custom-tenant-99, got %s", tc.TenantID())
	}
}
