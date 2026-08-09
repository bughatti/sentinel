-- 001_initial.sql — Sentinel NVR core schema
-- Event and recording schema, stable across releases.

-- ============================================================
-- cameras
-- ============================================================
CREATE TABLE IF NOT EXISTS cameras (
    name            TEXT        PRIMARY KEY,
    enabled         BOOLEAN     NOT NULL DEFAULT TRUE,
    width           INTEGER     NOT NULL DEFAULT 0,
    height          INTEGER     NOT NULL DEFAULT 0,
    fps             INTEGER     NOT NULL DEFAULT 0,
    config_json     JSONB       NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ============================================================
-- events
-- ============================================================
CREATE TABLE IF NOT EXISTS events (
    id              TEXT        PRIMARY KEY,          -- "unixtime.shortuuid"
    camera          TEXT        NOT NULL,
    label           TEXT        NOT NULL,
    sub_label       TEXT,                             -- e.g. face identity
    score           REAL        NOT NULL DEFAULT 0,
    false_positive  BOOLEAN     NOT NULL DEFAULT FALSE,

    -- Temporal bounds
    start_time      DOUBLE PRECISION NOT NULL,        -- Unix epoch seconds with sub-second
    end_time        DOUBLE PRECISION,
    has_clip        BOOLEAN     NOT NULL DEFAULT FALSE,
    has_snapshot    BOOLEAN     NOT NULL DEFAULT FALSE,
    retain_indefinitely BOOLEAN NOT NULL DEFAULT FALSE,

    -- Bounding box at best-score moment (normalised 0..1)
    top_score       REAL        NOT NULL DEFAULT 0,
    box             JSONB,                            -- {x1,y1,x2,y2} normalised
    region          JSONB,                            -- detection region within frame
    area            REAL        NOT NULL DEFAULT 0,   -- fraction of frame area

    -- Zones
    entered_zones   TEXT[]      NOT NULL DEFAULT '{}',
    current_zones   TEXT[]      NOT NULL DEFAULT '{}',

    -- Extra Sentinel data (raw detections, motion score, etc.)
    data            JSONB       NOT NULL DEFAULT '{}',

    -- detector fields
    thumbnail       BYTEA,
    model_hash      TEXT,
    detector_type   TEXT,
    model_type      TEXT,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_events_camera        ON events (camera);
CREATE INDEX IF NOT EXISTS idx_events_label         ON events (label);
CREATE INDEX IF NOT EXISTS idx_events_start_time    ON events (start_time DESC);
CREATE INDEX IF NOT EXISTS idx_events_end_time      ON events (end_time DESC);
CREATE INDEX IF NOT EXISTS idx_events_camera_label  ON events (camera, label);
CREATE INDEX IF NOT EXISTS idx_events_false_positive ON events (false_positive);
CREATE INDEX IF NOT EXISTS idx_events_has_clip      ON events (has_clip);
CREATE INDEX IF NOT EXISTS idx_events_has_snapshot  ON events (has_snapshot);

-- ============================================================
-- recordings
-- ============================================================
CREATE TABLE IF NOT EXISTS recordings (
    id              TEXT        PRIMARY KEY,   -- UUID
    camera          TEXT        NOT NULL,
    path            TEXT        NOT NULL UNIQUE,
    start_time      DOUBLE PRECISION NOT NULL,
    end_time        DOUBLE PRECISION NOT NULL,
    duration        REAL        NOT NULL DEFAULT 0,  -- seconds
    motion          BOOLEAN     NOT NULL DEFAULT FALSE,
    objects         TEXT[]      NOT NULL DEFAULT '{}',
    segment_size    BIGINT      NOT NULL DEFAULT 0,  -- bytes
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_recordings_camera       ON recordings (camera);
CREATE INDEX IF NOT EXISTS idx_recordings_start_time   ON recordings (start_time DESC);
CREATE INDEX IF NOT EXISTS idx_recordings_end_time     ON recordings (end_time DESC);
CREATE INDEX IF NOT EXISTS idx_recordings_camera_start ON recordings (camera, start_time DESC);
CREATE INDEX IF NOT EXISTS idx_recordings_motion       ON recordings (motion);

-- ============================================================
-- previews (low-res timelapse preview clips per hour)
-- ============================================================
CREATE TABLE IF NOT EXISTS previews (
    id              TEXT        PRIMARY KEY,
    camera          TEXT        NOT NULL,
    path            TEXT        NOT NULL UNIQUE,
    start_time      DOUBLE PRECISION NOT NULL,
    end_time        DOUBLE PRECISION NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_previews_camera     ON previews (camera);
CREATE INDEX IF NOT EXISTS idx_previews_start_time ON previews (start_time DESC);

-- ============================================================
-- snapshots
-- ============================================================
CREATE TABLE IF NOT EXISTS snapshots (
    id              TEXT        PRIMARY KEY,  -- event_id
    camera          TEXT        NOT NULL,
    path            TEXT        NOT NULL,
    score           REAL        NOT NULL DEFAULT 0,
    label           TEXT        NOT NULL,
    box             JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_snapshots_camera ON snapshots (camera);
CREATE INDEX IF NOT EXISTS idx_snapshots_label  ON snapshots (label);

-- ============================================================
-- zones (per-camera polygon definitions, for display / audit)
-- ============================================================
CREATE TABLE IF NOT EXISTS zones (
    id              SERIAL      PRIMARY KEY,
    camera          TEXT        NOT NULL,
    name            TEXT        NOT NULL,
    coordinates     TEXT        NOT NULL,   -- "x1,y1,x2,y2,..." raw string
    objects         TEXT[]      NOT NULL DEFAULT '{}',
    inertia         INTEGER     NOT NULL DEFAULT 3,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (camera, name)
);

CREATE INDEX IF NOT EXISTS idx_zones_camera ON zones (camera);
