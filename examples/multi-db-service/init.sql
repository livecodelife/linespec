CREATE TABLE IF NOT EXISTS orders (
    id          BIGINT AUTO_INCREMENT PRIMARY KEY,
    product_id  VARCHAR(255) NOT NULL,
    quantity    INT          NOT NULL,
    customer_id VARCHAR(255) NOT NULL,
    status      VARCHAR(50)  NOT NULL DEFAULT 'pending',
    created_at  TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
