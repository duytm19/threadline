#!/usr/bin/env bash
set -e

# Creates isolated logical databases and unprivileged roles per AD-4 and AD-6
# Security Invariants Enforced:
# 1. NOSUPERUSER, NOCREATEDB, NOCREATEROLE, NOREPLICATION, NOBYPASSRLS strictly set on all service roles.
# 2. REVOKE CONNECT ON DATABASE from PUBLIC prevents cross-service database access.
# 3. Dedicated service role is granted CONNECT and TEMPORARY strictly on its assigned database.
# 4. Schema public permissions revoked from PUBLIC; granted explicitly with least privilege to service role.
# 5. Default privileges configured so tables/sequences created during migrations inherit restricted DML grants.

function create_service_db() {
    local db=$1
    local user=$2
    local password=$3

    echo "Configuring unprivileged role '$user' and database '$db'..."

    # 1. Provision unprivileged role at PostgreSQL cluster level
    psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
        DO \$\$
        BEGIN
            IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = '$user') THEN
                CREATE ROLE $user WITH
                    LOGIN
                    PASSWORD '$password'
                    NOSUPERUSER
                    NOCREATEDB
                    NOCREATEROLE
                    NOREPLICATION
                    NOBYPASSRLS;
            ELSE
                ALTER ROLE $user WITH
                    LOGIN
                    PASSWORD '$password'
                    NOSUPERUSER
                    NOCREATEDB
                    NOCREATEROLE
                    NOREPLICATION
                    NOBYPASSRLS;
            END IF;
        END
        \$\$;

        -- Create database owned by service user if not exists
        SELECT 'CREATE DATABASE $db OWNER $user'
        WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '$db')\gexec

        -- AD-6: Disallow cross-database connections by revoking default PUBLIC grants
        REVOKE ALL ON DATABASE $db FROM PUBLIC;
        REVOKE CONNECT ON DATABASE $db FROM PUBLIC;

        -- Grant connection privileges strictly to the designated service role
        GRANT CONNECT, TEMPORARY ON DATABASE $db TO $user;
EOSQL

    # 2. Configure schema-level least-privilege grants inside the target database
    psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$db" <<-EOSQL
        -- Revoke public schema permissions from PUBLIC
        REVOKE ALL ON SCHEMA public FROM PUBLIC;
        REVOKE CREATE ON SCHEMA public FROM PUBLIC;

        -- Grant schema access to the service user
        GRANT USAGE, CREATE ON SCHEMA public TO $user;

        -- Grant DML table permissions to the service user
        GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO $user;
        GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO $user;

        -- Configure default privileges for future objects created in this schema
        ALTER DEFAULT PRIVILEGES IN SCHEMA public
            GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO $user;

        ALTER DEFAULT PRIVILEGES IN SCHEMA public
            GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO $user;
EOSQL
}

# Provision unprivileged worker role with no BYPASSRLS
function create_worker_role() {
    local db=$1
    local worker_user=$2
    local password=$3

    echo "Configuring unprivileged worker role '$worker_user' on database '$db'..."
    psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
        DO \$\$
        BEGIN
            IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = '$worker_user') THEN
                CREATE ROLE $worker_user WITH
                    LOGIN
                    PASSWORD '$password'
                    NOSUPERUSER
                    NOCREATEDB
                    NOCREATEROLE
                    NOREPLICATION
                    NOBYPASSRLS;
            ELSE
                ALTER ROLE $worker_user WITH
                    LOGIN
                    PASSWORD '$password'
                    NOSUPERUSER
                    NOCREATEDB
                    NOCREATEROLE
                    NOREPLICATION
                    NOBYPASSRLS;
            END IF;
        END
        \$\$;

        GRANT CONNECT, TEMPORARY ON DATABASE $db TO $worker_user;
EOSQL

    psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$db" <<-EOSQL
        GRANT USAGE ON SCHEMA public TO $worker_user;
        GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO $worker_user;
        GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO $worker_user;

        ALTER DEFAULT PRIVILEGES IN SCHEMA public
            GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO $worker_user;

        ALTER DEFAULT PRIVILEGES IN SCHEMA public
            GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO $worker_user;
EOSQL
}

# 1. Primary Microservice Databases and Unprivileged Roles
create_service_db "threadline_identity" "identity_user" "identity_pass"
create_service_db "threadline_catalog" "catalog_user" "catalog_pass"
create_service_db "threadline_inventory" "inventory_user" "inventory_pass"
create_service_db "threadline_order" "order_user" "order_pass"
create_service_db "threadline_payment" "payment_user" "payment_pass"
create_service_db "threadline_fulfillment" "fulfillment_user" "fulfillment_pass"

# 2. Dedicated Unprivileged Background Worker Roles (AD-4: zero BYPASSRLS)
create_worker_role "threadline_inventory" "inventory_worker" "inventory_worker_pass"
create_worker_role "threadline_order" "order_worker" "order_worker_pass"

echo "All Threadline service databases and unprivileged roles initialized successfully."
