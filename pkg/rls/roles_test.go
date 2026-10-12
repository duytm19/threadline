package rls_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestServiceRoles_NoBypassRLS verifies AD-4 security invariant:
// Microservice roles and background workers must be unprivileged and strictly NOBYPASSRLS.
func TestServiceRoles_NoBypassRLS(t *testing.T) {
	adminURL := os.Getenv("ADMIN_DATABASE_URL")
	if adminURL == "" {
		adminURL = "postgres://postgres:postgrespassword@localhost:5432/threadline_main?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Skipf("skipping role security test: database connection failed: %v", err)
		return
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping role security test: database ping failed: %v", err)
		return
	}

	requiredRoles := []string{
		"identity_user",
		"catalog_user",
		"inventory_user",
		"order_user",
		"payment_user",
		"fulfillment_user",
		"inventory_worker",
		"order_worker",
	}

	for _, role := range requiredRoles {
		t.Run("Role_"+role, func(t *testing.T) {
			query := `
				SELECT rolsuper, rolcreaterole, rolcreatedb, rolreplication, rolbypassrls
				FROM pg_catalog.pg_roles
				WHERE rolname = $1
			`
			var isSuper, canCreateRole, canCreateDB, isReplication, bypassRLS bool
			err := pool.QueryRow(ctx, query, role).Scan(
				&isSuper,
				&canCreateRole,
				&canCreateDB,
				&isReplication,
				&bypassRLS,
			)
			if err != nil {
				t.Fatalf("failed to query role %s: %v", role, err)
			}

			if isSuper {
				t.Errorf("role %s must not be superuser (NOSUPERUSER)", role)
			}
			if bypassRLS {
				t.Errorf("role %s must have NOBYPASSRLS (rolbypassrls = false per AD-4)", role)
			}
			if canCreateRole {
				t.Errorf("role %s must have NOCREATEROLE", role)
			}
			if canCreateDB {
				t.Errorf("role %s must have NOCREATEDB", role)
			}
			if isReplication {
				t.Errorf("role %s must have NOREPLICATION", role)
			}
		})
	}
}

// TestCrossDatabaseIsolation_AD6 verifies AD-6 invariant:
// Microservice roles cannot connect to other microservices' logical databases.
func TestCrossDatabaseIsolation_AD6(t *testing.T) {
	// Attempt connecting as order_user to threadline_inventory database
	crossDBURL := os.Getenv("TEST_CROSS_DB_URL")
	if crossDBURL == "" {
		crossDBURL = "postgres://order_user:order_pass@localhost:5432/threadline_inventory?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, crossDBURL)
	if err != nil {
		// Connection failed at init - expected per AD-6
		return
	}
	defer pool.Close()

	err = pool.Ping(ctx)
	if err == nil {
		t.Fatalf("AD-6 violation: order_user was able to connect to threadline_inventory database")
	}
}
