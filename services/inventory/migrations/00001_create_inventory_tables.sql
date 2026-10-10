-- +goose Up
-- Base migration establishing inventory schemas with enforced row-level security
CREATE TABLE IF NOT EXISTS stock_items (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     TEXT NOT NULL,
    sku           TEXT NOT NULL,
    warehouse_id  UUID NOT NULL,
    on_hand       INTEGER NOT NULL DEFAULT 0,
    reserved      INTEGER NOT NULL DEFAULT 0,
    damaged       INTEGER NOT NULL DEFAULT 0,
    version       INTEGER NOT NULL DEFAULT 0,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_non_negative CHECK (on_hand >= 0 AND reserved >= 0 AND damaged >= 0),
    CONSTRAINT chk_available_ge_zero CHECK (on_hand - reserved - damaged >= 0),
    CONSTRAINT uq_stock_item UNIQUE (tenant_id, sku, warehouse_id)
);

CREATE INDEX IF NOT EXISTS idx_stock_lookup ON stock_items (tenant_id, sku)
    INCLUDE (warehouse_id, on_hand, reserved, damaged);

CREATE INDEX IF NOT EXISTS idx_stock_low ON stock_items (tenant_id, sku)
    WHERE (on_hand - reserved - damaged) < 10;

ALTER TABLE stock_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_items FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON stock_items
    FOR ALL
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

CREATE TABLE IF NOT EXISTS reservations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   TEXT NOT NULL,
    order_id    UUID NOT NULL,
    status      TEXT NOT NULL CHECK (status IN ('ACTIVE', 'COMMITTED', 'RELEASED', 'EXPIRED')),
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_reservation_order UNIQUE (tenant_id, order_id)
);

CREATE INDEX IF NOT EXISTS idx_reservation_expiry ON reservations (expires_at)
    WHERE status = 'ACTIVE';

ALTER TABLE reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE reservations FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON reservations
    FOR ALL
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

-- +goose Down
DROP POLICY IF EXISTS tenant_isolation ON reservations;
ALTER TABLE reservations NO FORCE ROW LEVEL SECURITY;
ALTER TABLE reservations DISABLE ROW LEVEL SECURITY;
DROP TABLE IF EXISTS reservations;

DROP POLICY IF EXISTS tenant_isolation ON stock_items;
ALTER TABLE stock_items NO FORCE ROW LEVEL SECURITY;
ALTER TABLE stock_items DISABLE ROW LEVEL SECURITY;
DROP TABLE IF EXISTS stock_items;
