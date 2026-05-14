CREATE TABLE IF NOT EXISTS users (
    id         SERIAL PRIMARY KEY,
    email      TEXT NOT NULL,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS email_log (
    id         SERIAL PRIMARY KEY,
    user_id    INT NOT NULL,
    template   TEXT NOT NULL,
    recipient  TEXT NOT NULL,
    sent_at    TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS orders (
    id         SERIAL PRIMARY KEY,
    user_id    INT NOT NULL,
    amount     NUMERIC(10,2) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS reports (
    id           SERIAL PRIMARY KEY,
    user_id      INT NOT NULL,
    month        TEXT NOT NULL,
    total_orders INT NOT NULL,
    total_amount NUMERIC(10,2) NOT NULL
);
