-- 002_face_recognition.sql — pgvector extension + face identity store

-- Requires the pgvector extension (available in pgvector/pgvector Docker image).
CREATE EXTENSION IF NOT EXISTS vector;

-- face_identities stores named people and their 512-dim ArcFace embeddings.
-- Multiple embeddings per person are supported; search returns the best match
-- by cosine similarity.
CREATE TABLE IF NOT EXISTS face_identities (
    id          SERIAL      PRIMARY KEY,
    name        TEXT        NOT NULL,
    embedding   vector(512) NOT NULL,
    source_event_id TEXT    REFERENCES events(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_face_identities_name ON face_identities (name);

-- IVFFlat index for approximate nearest-neighbour cosine search.
-- lists=100 is appropriate for up to ~1 M rows; increase for larger datasets.
-- Rebuild with REINDEX when row count grows significantly.
CREATE INDEX IF NOT EXISTS idx_face_identities_embedding
    ON face_identities
    USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100);
