package rls_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/threadline/pkg/rls"
)

func initDockerHost() {
	if os.Getenv("DOCKER_HOST") == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			desktopSock := filepath.Join(home, ".docker", "desktop", "docker.sock")
			if _, err := os.Stat(desktopSock); err == nil {
				_ = os.Setenv("DOCKER_HOST", "unix://"+desktopSock)
			}
		}
	}
}

// startPostgresContainer spins up a PostgreSQL Testcontainer and provisions
// unprivileged service roles and RLS tables matching production schemas.
func startPostgresContainer(t *testing.T, ctx context.Context) (*tcpostgres.PostgresContainer, *pgxpool.Pool, *pgxpool.Pool) {
	initDockerHost()

	pgContainer, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("threadline_order"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgrespassword"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Skipf("skipping Testcontainers integration test (Docker unreachable): %v", err)
		return nil, nil, nil
	}

	adminConnStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to obtain admin connection string: %v", err)
	}

	adminPool, err := pgxpool.New(ctx, adminConnStr)
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to connect as admin: %v", err)
	}

	// 1. Provision unprivileged service role matching AD-4 / AD-6 invariants
	provisionSQL := `
		DO $$
		BEGIN
			IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'order_user') THEN
				CREATE ROLE order_user WITH
					LOGIN
					PASSWORD 'order_pass'
					NOSUPERUSER
					NOCREATEDB
					NOCREATEROLE
					NOREPLICATION
					NOBYPASSRLS;
			END IF;
		END
		$$;

		GRANT CONNECT, TEMPORARY ON DATABASE threadline_order TO order_user;
		GRANT USAGE, CREATE ON SCHEMA public TO order_user;

		-- 2. Establish orders schema with partition and RLS policies
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

		GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO order_user;
		GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO order_user;

		ALTER DEFAULT PRIVILEGES IN SCHEMA public
			GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO order_user;
	`
	if _, err := adminPool.Exec(ctx, provisionSQL); err != nil {
		adminPool.Close()
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to provision testcontainer database: %v", err)
	}

	// 3. Connect as unprivileged order_user
	host, err := pgContainer.Host(ctx)
	if err != nil {
		adminPool.Close()
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to get container host: %v", err)
	}
	port, err := pgContainer.MappedPort(ctx, "5432")
	if err != nil {
		adminPool.Close()
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to get container port: %v", err)
	}

	serviceConnStr := fmt.Sprintf("postgres://order_user:order_pass@%s:%s/threadline_order?sslmode=disable", host, port.Port())
	servicePool, err := pgxpool.New(ctx, serviceConnStr)
	if err != nil {
		adminPool.Close()
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to connect as order_user: %v", err)
	}

	return pgContainer, adminPool, servicePool
}

func TestRowLevelSecurity_Testcontainers_Integration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pgContainer, adminPool, servicePool := startPostgresContainer(t, ctx)
	if pgContainer == nil {
		return
	}
	defer func() {
		if servicePool != nil {
			servicePool.Close()
		}
		if adminPool != nil {
			adminPool.Close()
		}
		_ = pgContainer.Terminate(ctx)
	}()

	// Verify AD-4 security invariant on the service role
	t.Run("Security Invariant: order_user has NOBYPASSRLS", func(t *testing.T) {
		var isSuper, bypassRLS bool
		err := adminPool.QueryRow(ctx, `
			SELECT rolsuper, rolbypassrls
			FROM pg_catalog.pg_roles
			WHERE rolname = 'order_user'
		`).Scan(&isSuper, &bypassRLS)
		if err != nil {
			t.Fatalf("failed to inspect role: %v", err)
		}
		if isSuper {
			t.Fatal("order_user must not be superuser")
		}
		if bypassRLS {
			t.Fatal("order_user must have NOBYPASSRLS (rolbypassrls = false)")
		}
	})

	txManager := rls.NewTxManager(servicePool)

	tenantA := "zalora-vn"
	tenantB := "iconic-au"
	orderID_A := uuid.New()
	orderID_B := uuid.New()
	customerID := uuid.New()

	// Seed data under tenantA
	err := txManager.WithTenantTx(ctx, tenantA, func(ctx context.Context, tx pgx.Tx) error {
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
					t.Errorf("tenant isolation violation: expected %s, got leaked tenant %s", tenantA, tenant)
				}
				if id != orderID_A {
					t.Errorf("unexpected order ID: expected %s, got %s", orderID_A, id)
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
		conn, err := servicePool.Acquire(ctx)
		if err != nil {
			t.Fatalf("failed to acquire connection: %v", err)
		}
		defer conn.Release()

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
			t.Fatalf("fail-closed violated: expected 0 rows when app.tenant_id is unset, got %d", count)
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

	// Scenario 4: Connection Pool Cleanliness (No Lingering Tenant State)
	// Given a connection returned to the pool after tenant A transaction
	// When a raw query is executed on the connection
	// Then app.tenant_id must not leak and query must return zero rows
	t.Run("Scenario 4: Connection Pool Cleanliness (No Lingering Tenant State)", func(t *testing.T) {
		// Run tenant A transaction
		err := txManager.WithTenantTx(ctx, tenantA, func(ctx context.Context, tx pgx.Tx) error {
			var count int
			return tx.QueryRow(ctx, "SELECT COUNT(*) FROM orders").Scan(&count)
		})
		if err != nil {
			t.Fatalf("tenant A transaction failed: %v", err)
		}

		// Acquire connection from pool and verify app.tenant_id is reset/unset
		conn, err := servicePool.Acquire(ctx)
		if err != nil {
			t.Fatalf("failed to acquire connection: %v", err)
		}
		defer conn.Release()

		var currentSetting *string
		err = conn.QueryRow(ctx, "SELECT current_setting('app.tenant_id', true)").Scan(&currentSetting)
		if err != nil {
			t.Fatalf("failed to read current_setting: %v", err)
		}
		if currentSetting != nil && *currentSetting != "" {
			t.Fatalf("lingering tenant state detected on pooled connection: %s", *currentSetting)
		}
	})
}
