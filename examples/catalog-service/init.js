// MongoDB initialization script for catalog-service
// Executed by mongosh on first container start

db = db.getSiblingDB('catalog_service');

// Create products collection with a unique SKU index
db.createCollection('products');
db.products.createIndex({ sku: 1 }, { unique: true });
db.products.createIndex({ category: 1 });

// Seed a sample product so tests have a known starting state
db.products.insertOne({
    _id: ObjectId('507f1f77bcf86cd799439011'),
    name: 'Sample Widget',
    sku: 'SAMPLE-001',
    price: 9.99,
    stock: 100,
    category: 'General',
    created_at: new Date('2026-01-01T00:00:00Z'),
});
