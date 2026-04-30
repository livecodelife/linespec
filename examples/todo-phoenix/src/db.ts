import Database, { type Database as DatabaseType } from 'better-sqlite3';
import { existsSync, mkdirSync } from 'node:fs';
import { dirname } from 'node:path';

const DB_PATH = process.env.DB_PATH ?? 'data/app.db';

const dir = dirname(DB_PATH);
if (!existsSync(dir)) mkdirSync(dir, { recursive: true });

const db: DatabaseType = new Database(DB_PATH);
db.pragma('journal_mode = WAL');
db.pragma('foreign_keys = ON');

const migrations: Array<{ name: string; sql: string }> = [];

export function registerMigration(name: string, sql: string): void {
  migrations.push({ name, sql });
}

export function runMigrations(): void {
  for (const m of migrations) {
    db.exec(m.sql);
  }
}

export { db };

registerMigration('create_todos', `
  CREATE TABLE IF NOT EXISTS todos (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    title TEXT NOT NULL,
    description TEXT,
    completed INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
  )
`);
