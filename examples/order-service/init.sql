-- Create orders table
CREATE TABLE IF NOT EXISTS orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_name VARCHAR(255) NOT NULL,
    total_amount DECIMAL(10, 2) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Create index on created_at for ordering
CREATE INDEX IF NOT EXISTS idx_orders_created_at ON orders(created_at DESC);

-- Insert a test order
INSERT INTO orders (customer_name, total_amount, status) 
VALUES ('Test Customer', 99.99, 'completed')
ON CONFLICT DO NOTHING;
