-- 001_init: Core tables for y-ai-pond.
-- Enterprise_id / farm_id partition keys per plan guardrail.

CREATE TABLE IF NOT EXISTS farms (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(255) NOT NULL,
    location        VARCHAR(512) NOT NULL DEFAULT '',
    area_m2         DOUBLE PRECISION NOT NULL DEFAULT 0,
    species         VARCHAR(255) NOT NULL DEFAULT '',
    enterprise_id   VARCHAR(64) NOT NULL DEFAULT 'default',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS ponds (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    farm_id         UUID NOT NULL REFERENCES farms(id) ON DELETE CASCADE,
    name            VARCHAR(255) NOT NULL,
    area_m2         DOUBLE PRECISION NOT NULL DEFAULT 0,
    depth_m         DOUBLE PRECISION NOT NULL DEFAULT 0,
    fish_count      INTEGER NOT NULL DEFAULT 0,
    enterprise_id   VARCHAR(64) NOT NULL DEFAULT 'default',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ponds_farm_id ON ponds(farm_id);
CREATE INDEX IF NOT EXISTS idx_ponds_enterprise_id ON ponds(enterprise_id);

CREATE TABLE IF NOT EXISTS devices (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    farm_id          UUID NOT NULL REFERENCES farms(id) ON DELETE CASCADE,
    pond_id          UUID REFERENCES ponds(id) ON DELETE SET NULL,
    type             VARCHAR(64) NOT NULL,
    status           VARCHAR(32) NOT NULL DEFAULT 'offline',
    firmware_version VARCHAR(32) NOT NULL DEFAULT '',
    last_heartbeat   TIMESTAMPTZ,
    enterprise_id    VARCHAR(64) NOT NULL DEFAULT 'default',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_devices_farm_id ON devices(farm_id);
CREATE INDEX IF NOT EXISTS idx_devices_pond_id ON devices(pond_id);
CREATE INDEX IF NOT EXISTS idx_devices_enterprise_id ON devices(enterprise_id);

CREATE TABLE IF NOT EXISTS users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username        VARCHAR(128) NOT NULL UNIQUE,
    password_hash   VARCHAR(256) NOT NULL,
    role            VARCHAR(32) NOT NULL DEFAULT 'viewer',
    farm_ids        JSONB NOT NULL DEFAULT '[]',
    enterprise_id   VARCHAR(64) NOT NULL DEFAULT 'default',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
CREATE INDEX IF NOT EXISTS idx_users_enterprise_id ON users(enterprise_id);

CREATE TABLE IF NOT EXISTS feeding_logs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pond_id         UUID NOT NULL REFERENCES ponds(id) ON DELETE CASCADE,
    speed           DOUBLE PRECISION NOT NULL DEFAULT 0,
    duration        INTEGER NOT NULL DEFAULT 0,
    decision_json   JSONB NOT NULL DEFAULT '{}',
    enterprise_id   VARCHAR(64) NOT NULL DEFAULT 'default',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_feeding_logs_pond_id ON feeding_logs(pond_id);
CREATE INDEX IF NOT EXISTS idx_feeding_logs_created_at ON feeding_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_feeding_logs_enterprise_id ON feeding_logs(enterprise_id);

CREATE TABLE IF NOT EXISTS alerts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    farm_id         UUID NOT NULL REFERENCES farms(id) ON DELETE CASCADE,
    pond_id         UUID REFERENCES ponds(id) ON DELETE SET NULL,
    level           VARCHAR(16) NOT NULL DEFAULT 'INFO',
    type            VARCHAR(64) NOT NULL DEFAULT '',
    message         TEXT NOT NULL DEFAULT '',
    status          VARCHAR(16) NOT NULL DEFAULT 'open',
    enterprise_id   VARCHAR(64) NOT NULL DEFAULT 'default',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_alerts_farm_id ON alerts(farm_id);
CREATE INDEX IF NOT EXISTS idx_alerts_pond_id ON alerts(pond_id);
CREATE INDEX IF NOT EXISTS idx_alerts_status ON alerts(status);
CREATE INDEX IF NOT EXISTS idx_alerts_enterprise_id ON alerts(enterprise_id);
