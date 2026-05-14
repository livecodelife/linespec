CREATE TABLE IF NOT EXISTS users (
    id        INT PRIMARY KEY,
    email     TEXT NOT NULL,
    name      TEXT NOT NULL,
    synced_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
