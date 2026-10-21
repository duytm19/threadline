package rls

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/threadline/pkg/tenant"
)

var (
	// ErrMissingTenantID is returned when an operation requiring tenant scope lacks tenant context.
	ErrMissingTenantID = errors.New("rls: tenant_id must not be empty (fail-closed)")
)

// TxManager manages PostgreSQL transactions with strictly enforced Row-Level Security scoping.
type TxManager struct {
	pool *pgxpool.Pool
}

// NewTxManager creates a TxManager backed by pgxpool.
func NewTxManager(pool *pgxpool.Pool) *TxManager {
	return &TxManager{pool: pool}
}

// WithTenantTx executes a transaction scoped to a specific tenant via SET LOCAL app.tenant_id.
// It guarantees that:
// 1. The tenant_id parameter cannot be empty (fail-closed per AD-4).
// 2. SET LOCAL applies strictly to this transaction and evaporates upon commit/rollback.
// 3. Any error returned by fn triggers a rollback.
func (m *TxManager) WithTenantTx(ctx context.Context, tenantID string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if tenantID == "" {
		return ErrMissingTenantID
	}

	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("rls: failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx) // Safe no-op if tx was committed
	}()

	// Inject tenant context into local session variable
	_, err = tx.Exec(ctx, "SET LOCAL app.tenant_id = $1", tenantID)
	if err != nil {
		return fmt.Errorf("rls: failed to set local tenant context: %w", err)
	}

	// Execute business use case inside isolated tenant scope
	if err := fn(ctx, tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("rls: transaction commit failed: %w", err)
	}

	return nil
}

// WithTenantTxOptions allows custom transaction isolation level (e.g. Repeatable Read for Refunds).
func (m *TxManager) WithTenantTxOptions(ctx context.Context, tenantID string, opts pgx.TxOptions, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if tenantID == "" {
		return ErrMissingTenantID
	}

	tx, err := m.pool.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("rls: failed to begin tx with options: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	_, err = tx.Exec(ctx, "SET LOCAL app.tenant_id = $1", tenantID)
	if err != nil {
		return fmt.Errorf("rls: failed to set local tenant context: %w", err)
	}

	if err := fn(ctx, tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("rls: transaction commit failed: %w", err)
	}

	return nil
}

// WithContextTx executes a transaction by automatically resolving TenantContext from ctx.
// Fails closed if context lacks valid tenant information.
func (m *TxManager) WithContextTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tenantID, err := tenant.TenantIDFromContext(ctx)
	if err != nil {
		return fmt.Errorf("rls: %w", err)
	}
	return m.WithTenantTx(ctx, tenantID, fn)
}

// WithContextTxOptions executes a transaction with custom options by resolving TenantContext from ctx.
func (m *TxManager) WithContextTxOptions(ctx context.Context, opts pgx.TxOptions, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tenantID, err := tenant.TenantIDFromContext(ctx)
	if err != nil {
		return fmt.Errorf("rls: %w", err)
	}
	return m.WithTenantTxOptions(ctx, tenantID, opts, fn)
}
