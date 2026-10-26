package rls_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/threadline/pkg/rls"
	"github.com/threadline/pkg/tenant"
)

// mockTx implements pgx.Tx for deterministic unit testing.
type mockTx struct {
	execFn     func(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	commitFn   func(ctx context.Context) error
	rollbackFn func(ctx context.Context) error
	executed   []string
	committed  bool
	rolledBack bool
}

func (m *mockTx) Begin(ctx context.Context) (pgx.Tx, error) {
	return m, nil
}

func (m *mockTx) Commit(ctx context.Context) error {
	m.committed = true
	if m.commitFn != nil {
		return m.commitFn(ctx)
	}
	return nil
}

func (m *mockTx) Rollback(ctx context.Context) error {
	m.rolledBack = true
	if m.rollbackFn != nil {
		return m.rollbackFn(ctx)
	}
	return nil
}

func (m *mockTx) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	return 0, nil
}

func (m *mockTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	return nil
}

func (m *mockTx) LargeObjects() pgx.LargeObjects {
	return pgx.LargeObjects{}
}

func (m *mockTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	return nil, nil
}

func (m *mockTx) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	m.executed = append(m.executed, sql)
	if m.execFn != nil {
		return m.execFn(ctx, sql, arguments...)
	}
	return pgconn.NewCommandTag("SET"), nil
}

func (m *mockTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, nil
}

func (m *mockTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return nil
}

func (m *mockTx) Conn() *pgx.Conn {
	return nil
}

func TestSetLocalTenant_Success(t *testing.T) {
	tx := &mockTx{}
	ctx := context.Background()

	err := rls.SetLocalTenant(ctx, tx, "zalora-vn")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(tx.executed) != 1 {
		t.Fatalf("expected 1 statement executed, got %d", len(tx.executed))
	}
	expected := "SET LOCAL app.tenant_id = 'zalora-vn'"
	if tx.executed[0] != expected {
		t.Fatalf("expected query %q, got %q", expected, tx.executed[0])
	}
}

func TestSetLocalTenant_FailClosed(t *testing.T) {
	tx := &mockTx{}
	ctx := context.Background()

	tests := []struct {
		name     string
		tenantID string
		wantErr  error
		contains string
	}{
		{
			name:     "empty string",
			tenantID: "",
			wantErr:  rls.ErrMissingTenantID,
		},
		{
			name:     "whitespace only",
			tenantID: "   ",
			wantErr:  rls.ErrMissingTenantID,
		},
		{
			name:     "invalid characters (SQL injection attempt)",
			tenantID: "tenant; DROP TABLE orders;--",
			contains: "invalid tenant ID",
		},
		{
			name:     "invalid quote",
			tenantID: "' OR 1=1 --",
			contains: "invalid tenant ID",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := rls.SetLocalTenant(ctx, tx, tc.tenantID)
			if err == nil {
				t.Fatalf("expected error for tenant %q, got nil", tc.tenantID)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error %v, got %v", tc.wantErr, err)
			}
			if tc.contains != "" && !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("expected error to contain %q, got %v", tc.contains, err)
			}
		})
	}
}

func TestSetLocalTenant_ExecError(t *testing.T) {
	dbErr := errors.New("connection broken")
	tx := &mockTx{
		execFn: func(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
			return pgconn.CommandTag{}, dbErr
		},
	}

	err := rls.SetLocalTenant(context.Background(), tx, "zalora-vn")
	if err == nil || !errors.Is(err, dbErr) {
		t.Fatalf("expected database error wrapped, got %v", err)
	}
}

func TestNewTenantHook_ExplicitTenant(t *testing.T) {
	hook := rls.NewTenantHook("iconic-au")
	tx := &mockTx{}

	err := hook.OnTxStart(context.Background(), tx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(tx.executed) != 1 || tx.executed[0] != "SET LOCAL app.tenant_id = 'iconic-au'" {
		t.Fatalf("expected iconic-au SET LOCAL, got %v", tx.executed)
	}
}

func TestNewTenantHook_FromContext(t *testing.T) {
	hook := rls.NewTenantHook()
	tx := &mockTx{}

	// Missing tenant in context -> fail-closed
	err := hook.OnTxStart(context.Background(), tx)
	if err == nil || !strings.Contains(err.Error(), "missing tenant context") {
		t.Fatalf("expected missing tenant context error, got %v", err)
	}

	// Valid tenant in context
	ctx, err := tenant.WithTenantID(context.Background(), "the-iconic")
	if err != nil {
		t.Fatalf("failed to set tenant context: %v", err)
	}

	err = hook.OnTxStart(ctx, tx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(tx.executed) != 1 || tx.executed[0] != "SET LOCAL app.tenant_id = 'the-iconic'" {
		t.Fatalf("expected the-iconic SET LOCAL, got %v", tx.executed)
	}
}

func TestTenantMiddleware_Lifecycle(t *testing.T) {
	middleware := rls.TenantMiddleware()
	tx := &mockTx{}

	// 1. Missing context -> fail-closed without calling next
	called := false
	handler := middleware(func(ctx context.Context, tx pgx.Tx) error {
		called = true
		return nil
	})

	err := handler(context.Background(), tx)
	if err == nil || !strings.Contains(err.Error(), "missing tenant context") {
		t.Fatalf("expected missing tenant error, got %v", err)
	}
	if called {
		t.Fatal("handler should not have been called when tenant context missing")
	}

	// 2. Valid context -> sets local tenant and calls next
	ctx, _ := tenant.WithTenantID(context.Background(), "zalora-ph")
	err = handler(ctx, tx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("handler was not called")
	}
	if len(tx.executed) != 1 || tx.executed[0] != "SET LOCAL app.tenant_id = 'zalora-ph'" {
		t.Fatalf("expected zalora-ph SET LOCAL, got %v", tx.executed)
	}
}

func TestTenantIDMiddleware_Lifecycle(t *testing.T) {
	middleware := rls.TenantIDMiddleware("zalora-sg")
	tx := &mockTx{}

	called := false
	handler := middleware(func(ctx context.Context, tx pgx.Tx) error {
		called = true
		return nil
	})

	err := handler(context.Background(), tx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("handler should have been called")
	}
	if len(tx.executed) != 1 || tx.executed[0] != "SET LOCAL app.tenant_id = 'zalora-sg'" {
		t.Fatalf("expected zalora-sg SET LOCAL, got %v", tx.executed)
	}

	// Invalid tenantID -> fail-closed
	invalidMiddleware := rls.TenantIDMiddleware("")
	err = invalidMiddleware(func(ctx context.Context, tx pgx.Tx) error {
		return nil
	})(context.Background(), tx)
	if !errors.Is(err, rls.ErrMissingTenantID) {
		t.Fatalf("expected ErrMissingTenantID, got %v", err)
	}
}

func TestHookMiddleware(t *testing.T) {
	var trace []string

	hook1 := rls.TxHookFunc(func(ctx context.Context, tx pgx.Tx) error {
		trace = append(trace, "hook1")
		return nil
	})
	hook2 := rls.TxHookFunc(func(ctx context.Context, tx pgx.Tx) error {
		trace = append(trace, "hook2")
		return nil
	})

	middleware := rls.HookMiddleware(hook1, nil, hook2)
	tx := &mockTx{}

	err := middleware(func(ctx context.Context, tx pgx.Tx) error {
		trace = append(trace, "business_logic")
		return nil
	})(context.Background(), tx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []string{"hook1", "hook2", "business_logic"}
	if len(trace) != len(expected) {
		t.Fatalf("expected trace %v, got %v", expected, trace)
	}
	for i := range expected {
		if trace[i] != expected[i] {
			t.Fatalf("at index %d: expected %s, got %s", i, expected[i], trace[i])
		}
	}
}

func TestChain_Ordering(t *testing.T) {
	var order []string

	m1 := func(next rls.TxFunc) rls.TxFunc {
		return func(ctx context.Context, tx pgx.Tx) error {
			order = append(order, "m1_start")
			err := next(ctx, tx)
			order = append(order, "m1_end")
			return err
		}
	}

	m2 := func(next rls.TxFunc) rls.TxFunc {
		return func(ctx context.Context, tx pgx.Tx) error {
			order = append(order, "m2_start")
			err := next(ctx, tx)
			order = append(order, "m2_end")
			return err
		}
	}

	chained := rls.Chain(m1, m2)
	tx := &mockTx{}

	err := chained(func(ctx context.Context, tx pgx.Tx) error {
		order = append(order, "core")
		return nil
	})(context.Background(), tx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedOrder := []string{"m1_start", "m2_start", "core", "m2_end", "m1_end"}
	if len(order) != len(expectedOrder) {
		t.Fatalf("expected order %v, got %v", expectedOrder, order)
	}
	for i := range expectedOrder {
		if order[i] != expectedOrder[i] {
			t.Fatalf("at index %d: expected %s, got %s", i, expectedOrder[i], order[i])
		}
	}
}

func TestConfigurePool(t *testing.T) {
	// Should not panic on nil config
	rls.ConfigurePool(nil)

	cfg := &pgxpool.Config{}
	released := false
	cfg.AfterRelease = func(conn *pgx.Conn) bool {
		released = true
		return true
	}

	rls.ConfigurePool(cfg)
	if cfg.AfterRelease == nil {
		t.Fatal("expected AfterRelease to be set")
	}

	res := cfg.AfterRelease(nil)
	if !res || !released {
		t.Fatal("expected original AfterRelease to be executed")
	}
}
