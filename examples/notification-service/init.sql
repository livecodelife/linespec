--
-- PostgreSQL init script for notification service
--

-- Create database if it doesn't exist
SELECT 'CREATE DATABASE notification_service'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'notification_service')\gexec

\c notification_service;

-- Create notifications table
CREATE TABLE IF NOT EXISTS notifications (
    id SERIAL PRIMARY KEY,
    content VARCHAR(255) NOT NULL,
    recipient VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Create index on recipient for faster lookups
CREATE INDEX IF NOT EXISTS idx_notifications_recipient ON notifications(recipient);

-- Create index on created_at for sorting
CREATE INDEX IF NOT EXISTS idx_notifications_created_at ON notifications(created_at DESC);
