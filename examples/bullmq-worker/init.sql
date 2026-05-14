CREATE TABLE IF NOT EXISTS users (
    id         SERIAL PRIMARY KEY,
    email      TEXT NOT NULL,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS notification_log (
    id         SERIAL PRIMARY KEY,
    user_id    INT NOT NULL,
    channel    TEXT NOT NULL,
    message    TEXT NOT NULL,
    sent_at    TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS orders (
    id         SERIAL PRIMARY KEY,
    user_id    INT NOT NULL,
    amount     NUMERIC(10,2) NOT NULL,
    product    TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS invoices (
    id        SERIAL PRIMARY KEY,
    user_id   INT NOT NULL,
    order_id  INT NOT NULL,
    amount    NUMERIC(10,2) NOT NULL,
    issued_at TIMESTAMPTZ NOT NULL
);
