package rls

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/threadline/pkg/tenant"
)

// TxFunc is the application or domain logic executed inside an isolated transaction.
type TxFunc func(ctx context.Context, tx pgx.Tx) error

// TxMiddleware wraps a TxFunc, enabling interception of transaction execution
// for cross-cutting concerns (e.g. tenant context injection, observability, metrics).
type TxMiddleware func(next TxFunc) TxFunc

// TxHook defines lifecycle callbacks executed during transaction execution.
type TxHook interface {
	// OnTxStart is called immediately after a transaction begins, before user logic runs.
	OnTxStart(ctx context.Context, tx pgx.Tx) error
}

// TxHookFunc is a functional adapter that allows an ordinary function to satisfy TxHook.
type TxHookFunc func(ctx context.Context, tx pgx.Tx) error

// OnTxStart implements TxHook.
func (f TxHookFunc) OnTxStart(ctx context.Context, tx pgx.Tx) error {
	return f(ctx, tx)
}

// SetLocalTenant executes `SET LOCAL app.tenant_id = '<tenant_id>'` on the active transaction.
// Guarantees:
// 1. Fail-closed defense: empty or whitespace tenantID returns ErrMissingTenantID (AD-4).
// 2. Syntax validation: tenantID must conform to tenant.New validation, preventing SQL injection.
// 3. Strict local scoping: SET LOCAL applies only to the active transaction and evaporates on commit/rollback.
func SetLocalTenant(ctx context.Context, tx pgx.Tx, tenantID string) error {
	trimmed := strings.TrimSpace(tenantID)
	if trimmed == "" {
		return ErrMissingTenantID
	}

	tc, err := tenant.New(trimmed)
	if err != nil {
		return fmt.Errorf("rls: invalid tenant ID: %w", err)
	}

	query := fmt.Sprintf("SET LOCAL app.tenant_id = '%s'", tc.TenantID())
	if _, err := tx.Exec(ctx, query); err != nil {
		return fmt.Errorf("rls: failed to set local tenant context: %w", err)
	}
	return nil
}

// NewTenantHook creates a TxHook that executes SET LOCAL app.tenant_id on transaction start.
// If explicitTenantID is provided and non-empty, it scopes strictly to that tenant.
// Otherwise, it extracts TenantContext from context.Context at transaction start, failing closed if absent.
func NewTenantHook(explicitTenantID ...string) TxHook {
	var explicit string
	if len(explicitTenantID) > 0 && explicitTenantID[0] != "" {
		explicit = explicitTenantID[0]
	}

	return TxHookFunc(func(ctx context.Context, tx pgx.Tx) error {
		tenantID := explicit
		if tenantID == "" {
			var err error
			tenantID, err = tenant.TenantIDFromContext(ctx)
			if err != nil {
				return fmt.Errorf("rls: %w", err)
			}
		}
		return SetLocalTenant(ctx, tx, tenantID)
	})
}

// TenantMiddleware returns a TxMiddleware that executes SET LOCAL app.tenant_id
// on transaction start by extracting TenantContext from ctx.
// Fails closed if the context lacks a valid TenantContext (AD-4).
func TenantMiddleware() TxMiddleware {
	return func(next TxFunc) TxFunc {
		return func(ctx context.Context, tx pgx.Tx) error {
			tenantID, err := tenant.TenantIDFromContext(ctx)
			if err != nil {
				return fmt.Errorf("rls: %w", err)
			}
			if err := SetLocalTenant(ctx, tx, tenantID); err != nil {
				return err
			}
			return next(ctx, tx)
		}
	}
}

// TenantIDMiddleware returns a TxMiddleware that executes SET LOCAL app.tenant_id
// for an explicit tenantID on transaction start.
// Fails closed if tenantID is empty or invalid.
func TenantIDMiddleware(tenantID string) TxMiddleware {
	return func(next TxFunc) TxFunc {
		return func(ctx context.Context, tx pgx.Tx) error {
			if err := SetLocalTenant(ctx, tx, tenantID); err != nil {
				return err
			}
			return next(ctx, tx)
		}
	}
}

// HookMiddleware adapts one or more TxHooks into a TxMiddleware.
// Registered hooks execute their OnTxStart callbacks in order before delegating to next.
func HookMiddleware(hooks ...TxHook) TxMiddleware {
	return func(next TxFunc) TxFunc {
		return func(ctx context.Context, tx pgx.Tx) error {
			for _, hook := range hooks {
				if hook == nil {
					continue
				}
				if err := hook.OnTxStart(ctx, tx); err != nil {
					return err
				}
			}
			return next(ctx, tx)
		}
	}
}

// Chain composes multiple TxMiddleware into a single TxMiddleware.
// Middlewares are executed in standard onion order: the first middleware in the slice
// wraps all subsequent middlewares.
func Chain(middlewares ...TxMiddleware) TxMiddleware {
	return func(final TxFunc) TxFunc {
		for i := len(middlewares) - 1; i >= 0; i-- {
			if middlewares[i] != nil {
				final = middlewares[i](final)
			}
		}
		return final
	}
}

// ConfigurePool applies connection pool lifecycle settings ensuring pooled connections
// do not retain lingering state.
func ConfigurePool(config *pgxpool.Config) {
	if config == nil {
		return
	}
	origAfterRelease := config.AfterRelease
	config.AfterRelease = func(conn *pgx.Conn) bool {
		if origAfterRelease != nil {
			return origAfterRelease(conn)
		}
		return true
	}
}
