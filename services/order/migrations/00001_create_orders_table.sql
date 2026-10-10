-- +goose Up
-- Base migration establishing orders schema with enforced row-level security
CREATE TABLE IF NOT EXISTS orders (
    id              UUID NOT NULL DEFAULT gen_random_uuid(),
    tenant_id       TEXT NOT NULL,
    customer_id     UUID NOT NULL,
    status          TEXT NOT NULL,
    total_minor     BIGINT NOT NULL CHECK (total_minor >= 0),
    currency        CHAR(3) NOT NULL,
    version         INTEGER NOT NULL DEFAULT 0,
    placed_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, placed_at)
) PARTITION BY RANGE (placed_at);

CREATE INDEX IF NOT EXISTS idx_orders_customer ON orders (tenant_id, customer_id, placed_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_active ON orders (tenant_id, status, placed_at DESC)
    WHERE status IN ('CREATED', 'RESERVED', 'PAID', 'ALLOCATED');

CREATE TABLE IF NOT EXISTS orders_default PARTITION OF orders DEFAULT;

ALTER TABLE orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE orders FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON orders
    FOR ALL
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

-- +goose Down
DROP POLICY IF EXISTS tenant_isolation ON orders;
ALTER TABLE orders NO FORCE ROW LEVEL SECURITY;
ALTER TABLE orders DISABLE ROW LEVEL SECURITY;
DROP TABLE IF EXISTS orders_default;
DROP TABLE IF EXISTS orders;
