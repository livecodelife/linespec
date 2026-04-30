import { Hono } from 'hono';
import { db, registerMigration } from '../../db.js';
import { z } from 'zod';

// ─── Database migrations ────────────────────────────────────────────────────

// ─── Database migrations ────────────────────────────────────────────────────
registerMigration('todos', `
  CREATE TABLE IF NOT EXISTS todos (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    completed INTEGER NOT NULL DEFAULT 0,
    user_id TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
  )
`);

// ─── Schemas ────────────────────────────────────────────────────────────────
const CreateTodoSchema = z.object({
  title: z.string().min(1, 'title is required'),
  description: z.string().optional().default(''),
  completed: z.boolean().optional().default(false),
});

const UpdateTodoSchema = z.object({
  title: z.string().min(1, 'title is required').optional(),
  description: z.string().optional(),
  completed: z.boolean().optional(),
});

// ─── Routes ─────────────────────────────────────────────────────────────────
const router = new Hono();

// Auth middleware
router.use('*', async (c, next) => {
  const authHeader = c.req.header('authorization');
  if (!authHeader) {
    return c.json({ error: 'Unauthorized' }, 401);
  }
  const userServiceUrl = process.env.USER_SERVICE_URL;
  if (!userServiceUrl) {
    return c.json({ error: 'Unauthorized' }, 401);
  }
  try {
    const authResponse = await fetch(userServiceUrl, {
      headers: { authorization: authHeader },
    });
    if (authResponse.status !== 200) {
      return c.json({ error: 'Unauthorized' }, 401);
    }
    const authData = (await authResponse.json()) as { id: string };
    c.set('userId', authData.id);
    await next();
  } catch {
    return c.json({ error: 'Unauthorized' }, 401);
  }
});

// List todos
router.get('/', (c) => {
  const userId = c.get('userId') as string;
  const completed = c.req.query('completed');

  let sql = 'SELECT id, title, description, completed, user_id, created_at, updated_at FROM todos WHERE user_id = ?';
  const params: (string | number)[] = [userId];

  if (completed !== undefined) {
    sql += ' AND completed = ?';
    params.push(completed === 'true' ? 1 : 0);
  }

  sql += ' ORDER BY created_at DESC';

  const todos = db.prepare(sql).all(...params) as Record<string, unknown>[];
  return c.json(todos.map((t) => ({ ...t, completed: Boolean(t.completed) })));
});

// Create todo
router.post('/', async (c) => {
  const userId = c.get('userId') as string;

  let body;
  try {
    body = await c.req.json();
  } catch {
    return c.json({ error: 'Invalid JSON' }, 400);
  }

  const result = CreateTodoSchema.safeParse(body);
  if (!result.success) {
    const message = result.error.issues[0].message;
    return c.json({ error: message }, 400);
  }

  const { title, description, completed } = result.data;
  const info = db.prepare(
    "INSERT INTO todos (title, description, completed, user_id) VALUES (?, ?, ?, ?)"
  ).run(title, description, completed ? 1 : 0, userId);

  const todo = db.prepare(
    "SELECT id, title, description, completed, user_id, created_at, updated_at FROM todos WHERE id = ?"
  ).get(info.lastInsertRowid) as Record<string, unknown> | undefined;

  if (!todo) {
    return c.json({ error: 'Failed to create todo' }, 500);
  }

  return c.json({ ...todo, completed: Boolean(todo.completed) }, 201);
});

// Get todo
router.get('/:id', (c) => {
  const userId = c.get('userId') as string;
  const id = c.req.param('id');

  const todo = db.prepare(
    "SELECT id, title, description, completed, user_id, created_at, updated_at FROM todos WHERE id = ? AND user_id = ?"
  ).get(id, userId) as Record<string, unknown> | undefined;

  if (!todo) {
    return c.json({ error: 'Todo not found' }, 404);
  }

  return c.json({ ...todo, completed: Boolean(todo.completed) });
});

// Update todo
router.patch('/:id', async (c) => {
  const userId = c.get('userId') as string;
  const id = c.req.param('id');

  const existing = db.prepare(
    "SELECT id FROM todos WHERE id = ? AND user_id = ?"
  ).get(id, userId) as Record<string, unknown> | undefined;

  if (!existing) {
    return c.json({ error: 'Todo not found' }, 404);
  }

  let body;
  try {
    body = await c.req.json();
  } catch {
    return c.json({ error: 'Invalid JSON' }, 400);
  }

  const result = UpdateTodoSchema.safeParse(body);
  if (!result.success) {
    const message = result.error.issues[0].message;
    return c.json({ error: message }, 400);
  }

  const updates: string[] = [];
  const params: (string | number)[] = [];

  if (result.data.title !== undefined) {
    updates.push('title = ?');
    params.push(result.data.title);
  }
  if (result.data.description !== undefined) {
    updates.push('description = ?');
    params.push(result.data.description);
  }
  if (result.data.completed !== undefined) {
    updates.push('completed = ?');
    params.push(result.data.completed ? 1 : 0);
  }

  if (updates.length === 0) {
    const todo = db.prepare(
      "SELECT id, title, description, completed, user_id, created_at, updated_at FROM todos WHERE id = ?"
    ).get(id) as Record<string, unknown>;
    return c.json({ ...todo, completed: Boolean(todo.completed) });
  }

  updates.push("updated_at = datetime('now')");
  params.push(id);
  params.push(userId);

  db.prepare(
    `UPDATE todos SET ${updates.join(', ')} WHERE id = ? AND user_id = ?`
  ).run(...params);

  const todo = db.prepare(
    "SELECT id, title, description, completed, user_id, created_at, updated_at FROM todos WHERE id = ?"
  ).get(id) as Record<string, unknown>;

  return c.json({ ...todo, completed: Boolean(todo.completed) });
});

// Delete todo
router.delete('/:id', (c) => {
  const userId = c.get('userId') as string;
  const id = c.req.param('id');

  const existing = db.prepare(
    "SELECT id FROM todos WHERE id = ? AND user_id = ?"
  ).get(id, userId) as Record<string, unknown> | undefined;

  if (!existing) {
    return c.json({ error: 'Todo not found' }, 404);
  }

  db.prepare("DELETE FROM todos WHERE id = ? AND user_id = ?").run(id, userId);

  return c.body(null, 204 as any);
});

/** @internal Phoenix VCS traceability — do not remove. */


export default router;

/** @internal Phoenix VCS traceability — do not remove. */
export const _phoenix = {
  iu_id: 'e3eddeb7a82aad689d0fefd0f28a3162d65312f9528d35d8782395fae55f5c00',
  name: 'Create Todo',
  risk_tier: 'high',
  canon_ids: [5 as const],
} as const;
