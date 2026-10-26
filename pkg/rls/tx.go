package rls

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/threadline/pkg/tenant"
)

var (
	// ErrMissingTenantID is returned when an operation requiring tenant scope lacks tenant context (fail-closed per AD-4).
	ErrMissingTenantID = errors.New("rls: tenant_id must not be empty (fail-closed)")

	// ErrNilPool is returned when a transaction operation is executed against a nil connection pool.
	ErrNilPool = errors.New("rls: database pool is nil")
)

// Transactor represents any database connection pool capable of initiating transactions (e.g. *pgxpool.Pool).
type Transactor interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}

// TransactionManager defines the port for managing scoped database transactions with RLS isolation.
type TransactionManager interface {
	WithContextTx(ctx context.Context, fn TxFunc) error
	WithContextTxOptions(ctx context.Context, opts pgx.TxOptions, fn TxFunc) error
	WithTenantTx(ctx context.Context, tenantID string, fn func(ctx context.Context, tx pgx.Tx) error) error
	WithTenantTxOptions(ctx context.Context, tenantID string, opts pgx.TxOptions, fn func(ctx context.Context, tx pgx.Tx) error) error
	WithTx(ctx context.Context, fn TxFunc, middlewares ...TxMiddleware) error
	WithTxOptions(ctx context.Context, opts pgx.TxOptions, fn TxFunc, middlewares ...TxMiddleware) error
}

// TxManager manages PostgreSQL transactions with strictly enforced Row-Level Security scoping.
type TxManager struct {
	pool        Transactor
	middlewares []TxMiddleware
	hooks       []TxHook
}

// TxManagerOption configures a TxManager instance.
type TxManagerOption func(*TxManager)

// WithMiddleware appends default transaction middlewares executed on every transaction.
func WithMiddleware(middlewares ...TxMiddleware) TxManagerOption {
	return func(m *TxManager) {
		m.middlewares = append(m.middlewares, middlewares...)
	}
}

// WithHooks appends default transaction hooks executed on transaction start.
func WithHooks(hooks ...TxHook) TxManagerOption {
	return func(m *TxManager) {
		m.hooks = append(m.hooks, hooks...)
	}
}

// NewTxManager creates a TxManager backed by pgxpool.Pool (or any Transactor).
func NewTxManager(pool Transactor, opts ...TxManagerOption) *TxManager {
	m := &TxManager{
		pool: pool,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// WithTenantTx executes a transaction scoped to a specific tenant via SET LOCAL app.tenant_id.
// It guarantees that:
// 1. The tenant_id parameter cannot be empty (fail-closed per AD-4).
// 2. SET LOCAL applies strictly to this transaction and evaporates upon commit/rollback.
// 3. Any error returned by fn triggers a rollback.
func (m *TxManager) WithTenantTx(ctx context.Context, tenantID string, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return m.WithTenantTxOptions(ctx, tenantID, pgx.TxOptions{}, fn)
}

// WithTenantTxOptions allows custom transaction isolation level (e.g. Repeatable Read for Refunds).
func (m *TxManager) WithTenantTxOptions(ctx context.Context, tenantID string, opts pgx.TxOptions, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if tenantID == "" {
		return ErrMissingTenantID
	}
	return m.WithTxOptions(ctx, opts, fn, TenantIDMiddleware(tenantID))
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

// WithTx executes fn inside a transaction wrapped by registered and invocation middlewares.
func (m *TxManager) WithTx(ctx context.Context, fn TxFunc, middlewares ...TxMiddleware) error {
	return m.WithTxOptions(ctx, pgx.TxOptions{}, fn, middlewares...)
}

// WithTxOptions executes fn inside a transaction with custom options and middleware chain.
func (m *TxManager) WithTxOptions(ctx context.Context, opts pgx.TxOptions, fn TxFunc, middlewares ...TxMiddleware) error {
	if m.pool == nil {
		return ErrNilPool
	}

	tx, err := m.pool.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("rls: failed to begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx) // Safe no-op if tx was committed
	}()

	allMiddlewares := make([]TxMiddleware, 0, len(m.middlewares)+len(middlewares)+1)
	if len(m.hooks) > 0 {
		allMiddlewares = append(allMiddlewares, HookMiddleware(m.hooks...))
	}
	allMiddlewares = append(allMiddlewares, m.middlewares...)
	allMiddlewares = append(allMiddlewares, middlewares...)

	pipeline := Chain(allMiddlewares...)(fn)

	if err := pipeline(ctx, tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("rls: transaction commit failed: %w", err)
	}

	return nil
}
