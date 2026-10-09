#!/usr/bin/env bash
set -e

# Creates isolated logical databases and unprivileged users per AD-4 and AD-6
function create_service_db() {
    local db=$1
    local user=$2
    local password=$3

    echo "Initializing database '$db' with unprivileged user '$user'..."
    psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
        DO \$\$
        BEGIN
            IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = '$user') THEN
                CREATE ROLE $user WITH LOGIN PASSWORD '$password' NOBYPASSRLS;
            END IF;
        END
        \$\$;

        SELECT 'CREATE DATABASE $db OWNER $user'
        WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '$db')\gexec

        GRANT ALL PRIVILEGES ON DATABASE $db TO $user;
EOSQL
}

create_service_db "threadline_identity" "identity_user" "identity_pass"
create_service_db "threadline_catalog" "catalog_user" "catalog_pass"
create_service_db "threadline_inventory" "inventory_user" "inventory_pass"
create_service_db "threadline_order" "order_user" "order_pass"
create_service_db "threadline_payment" "payment_user" "payment_pass"
create_service_db "threadline_fulfillment" "fulfillment_user" "fulfillment_pass"

echo "All Threadline service databases and roles initialized successfully."
