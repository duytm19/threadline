package rls_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/threadline/pkg/rls"
)

func TestTxManager_FailClosedOnEmptyTenant(t *testing.T) {
	manager := rls.NewTxManager(nil)

	err := manager.WithTenantTx(context.Background(), "", func(ctx context.Context, tx pgx.Tx) error {
		return nil
	})
	if !errors.Is(err, rls.ErrMissingTenantID) {
		t.Fatalf("expected ErrMissingTenantID, got %v", err)
	}

	err = manager.WithTenantTxOptions(context.Background(), "", pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		return nil
	})
	if !errors.Is(err, rls.ErrMissingTenantID) {
		t.Fatalf("expected ErrMissingTenantID, got %v", err)
	}
}

func TestRowLevelSecurity_BDDScenarios(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://order_user:order_pass@localhost:5432/threadline_order?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("skipping integration test: database connection failed: %v", err)
		return
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Skipf("skipping integration test: database ping failed: %v", err)
		return
	}

	// Schema setup matching 00001_create_orders_table.sql migration
	migrationSQL := `
		CREATE TABLE IF NOT EXISTS orders (
			id          UUID NOT NULL,
			tenant_id   TEXT NOT NULL,
			customer_id UUID NOT NULL,
			status      TEXT NOT NULL,
			total_minor BIGINT NOT NULL CHECK (total_minor >= 0),
			currency    CHAR(3) NOT NULL,
			version     INTEGER NOT NULL DEFAULT 0,
			placed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (id, placed_at)
		) PARTITION BY RANGE (placed_at);

		CREATE TABLE IF NOT EXISTS orders_default PARTITION OF orders DEFAULT;

		ALTER TABLE orders ENABLE ROW LEVEL SECURITY;
		ALTER TABLE orders FORCE ROW LEVEL SECURITY;

		DROP POLICY IF EXISTS tenant_isolation ON orders;
		CREATE POLICY tenant_isolation ON orders
			FOR ALL
			USING (tenant_id = current_setting('app.tenant_id', true))
			WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
	`
	if _, err := pool.Exec(ctx, migrationSQL); err != nil {
		t.Fatalf("failed to apply base migration: %v", err)
	}

	txManager := rls.NewTxManager(pool)

	tenantA := "zalora-vn"
	tenantB := "iconic-au"
	orderID_A := uuid.New()
	orderID_B := uuid.New()
	customerID := uuid.New()

	// Seed data under tenantA
	err = txManager.WithTenantTx(ctx, tenantA, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO orders (id, tenant_id, customer_id, status, total_minor, currency)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, orderID_A, tenantA, customerID, "CREATED", 100000, "VND")
		return err
	})
	if err != nil {
		t.Fatalf("failed to seed tenant A order: %v", err)
	}

	// Seed data under tenantB
	err = txManager.WithTenantTx(ctx, tenantB, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO orders (id, tenant_id, customer_id, status, total_minor, currency)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, orderID_B, tenantB, customerID, "CREATED", 15000, "AUD")
		return err
	})
	if err != nil {
		t.Fatalf("failed to seed tenant B order: %v", err)
	}

	// Scenario 1: Authorized Tenant Query Execution
	// Given database contains orders for tenant zalora-vn and iconic-au
	// When transaction sets SET LOCAL app.tenant_id = 'zalora-vn' and queries SELECT * FROM orders
	// Then only orders matching tenant_id = 'zalora-vn' are returned
	t.Run("Scenario 1: Authorized Tenant Query Execution", func(t *testing.T) {
		err := txManager.WithTenantTx(ctx, tenantA, func(ctx context.Context, tx pgx.Tx) error {
			rows, err := tx.Query(ctx, "SELECT id, tenant_id FROM orders")
			if err != nil {
				return err
			}
			defer rows.Close()

			var count int
			for rows.Next() {
				var id uuid.UUID
				var tenant string
				if err := rows.Scan(&id, &tenant); err != nil {
					return err
				}
				if tenant != tenantA {
					t.Errorf("expected tenant %s, got leaked tenant %s", tenantA, tenant)
				}
				if id != orderID_A {
					t.Errorf("unexpected order ID %s", id)
				}
				count++
			}
			if count != 1 {
				t.Errorf("expected exactly 1 order for %s, got %d", tenantA, count)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scenario 1 failed: %v", err)
		}
	})

	// Scenario 2: Unset Tenant Context Defense (Fail-Closed)
	// Given a database connection where app.tenant_id has not been set
	// When any query on orders is executed
	// Then zero rows are returned (fail-closed security)
	t.Run("Scenario 2: Unset Tenant Context Defense (Fail-Closed)", func(t *testing.T) {
		conn, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatalf("failed to acquire connection: %v", err)
		}
		defer conn.Release()

		// Begin raw transaction without SET LOCAL app.tenant_id
		rawTx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("failed to begin raw transaction: %v", err)
		}
		defer func() { _ = rawTx.Rollback(ctx) }()

		var count int
		err = rawTx.QueryRow(ctx, "SELECT COUNT(*) FROM orders").Scan(&count)
		if err != nil {
			t.Fatalf("query failed: %v", err)
		}
		if count != 0 {
			t.Errorf("fail-closed violated: expected 0 rows when app.tenant_id unset, got %d", count)
		}
	})

	// Scenario 3: Cross-Tenant Data Injection Prevention
	// Given an active transaction set to tenant_id = 'zalora-vn'
	// When an INSERT INTO orders with tenant_id = 'iconic-au' is attempted
	// Then PostgreSQL rejects the statement with RLS violation error 42501
	t.Run("Scenario 3: Cross-Tenant Data Injection Prevention", func(t *testing.T) {
		err := txManager.WithTenantTx(ctx, tenantA, func(ctx context.Context, tx pgx.Tx) error {
			maliciousOrderID := uuid.New()
			_, err := tx.Exec(ctx, `
				INSERT INTO orders (id, tenant_id, customer_id, status, total_minor, currency)
				VALUES ($1, $2, $3, $4, $5, $6)
			`, maliciousOrderID, tenantB, customerID, "CREATED", 9999, "AUD")
			return err
		})

		if err == nil {
			t.Fatal("expected RLS violation error, got nil")
		}

		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code != "42501" {
				t.Errorf("expected SQLSTATE 42501 (insufficient_privilege), got %s", pgErr.Code)
			}
		} else if !strings.Contains(err.Error(), "row-level security") {
			t.Errorf("expected error to mention row-level security, got %v", err)
		}
	})
}
