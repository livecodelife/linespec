import { Hono } from 'hono';
import { db, registerMigration } from '../../db.js';
import { z } from 'zod';

// ─── Database migrations ────────────────────────────────────────────────────

// ─── Database migrations ────────────────────────────────────────────────────
const router = new Hono();

registerMigration('todos', `
  CREATE TABLE IF NOT EXISTS todos (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT,
    completed INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
  )
`);

const UpdateTodoSchema = z.object({
  title: z.string().min(1).optional(),
  description: z.string().nullable().optional(),
  completed: z.boolean().optional(),
});

router.patch('/:id', async (c) => {
  const authHeader = c.req.header('Authorization');
  if (!authHeader) {
    return c.json({ error: 'Unauthorized' }, 401);
  }

  const userServiceUrl = process.env.USER_SERVICE_URL;
  if (!userServiceUrl) {
    return c.json({ error: 'Unauthorized' }, 401);
  }

  let userId: string;
  try {
    const authRes = await fetch(userServiceUrl, {
      headers: { Authorization: authHeader },
    });
    if (authRes.status !== 200) {
      return c.json({ error: 'Unauthorized' }, 401);
    }
    const authData = (await authRes.json()) as { id: string };
    userId = authData.id;
  } catch {
    return c.json({ error: 'Unauthorized' }, 401);
  }

  const id = c.req.param('id');
  const existing = db.prepare('SELECT * FROM todos WHERE id = ? AND user_id = ?').get(id, userId) as Record<string, unknown> | undefined;
  if (!existing) {
    return c.json({ error: 'Not found' }, 404);
  }

  let body;
  try {
    body = await c.req.json();
  } catch {
    return c.json({ error: 'Invalid JSON' }, 400);
  }

  const result = UpdateTodoSchema.safeParse(body);
  if (!result.success) {
    return c.json({ error: result.error.issues[0].message }, 400);
  }

  const u = result.data;
  const updates: string[] = [];
  const params: unknown[] = [];

  if (u.title !== undefined) {
    updates.push('title = ?');
    params.push(u.title);
  }
  if (u.description !== undefined) {
    updates.push('description = ?');
    params.push(u.description);
  }
  if (u.completed !== undefined) {
    updates.push('completed = ?');
    params.push(u.completed ? 1 : 0);
  }

  if (updates.length > 0) {
    updates.push("updated_at = datetime('now')");
    params.push(id);
    params.push(userId);
    db.prepare(`UPDATE todos SET ${updates.join(', ')} WHERE id = ? AND user_id = ?`).run(...params);
  }

  const updated = db.prepare('SELECT * FROM todos WHERE id = ? AND user_id = ?').get(id, userId);
  return c.json(updated, 200);
});

router.delete('/:id', async (c) => {
  const authHeader = c.req.header('Authorization');
  if (!authHeader) {
    return c.json({ error: 'Unauthorized' }, 401);
  }

  const userServiceUrl = process.env.USER_SERVICE_URL;
  if (!userServiceUrl) {
    return c.json({ error: 'Unauthorized' }, 401);
  }

  let userId: string;
  try {
    const authRes = await fetch(userServiceUrl, {
      headers: { Authorization: authHeader },
    });
    if (authRes.status !== 200) {
      return c.json({ error: 'Unauthorized' }, 401);
    }
    const authData = (await authRes.json()) as { id: string };
    userId = authData.id;
  } catch {
    return c.json({ error: 'Unauthorized' }, 401);
  }

  const id = c.req.param('id');
  const existing = db.prepare('SELECT * FROM todos WHERE id = ? AND user_id = ?').get(id, userId) as Record<string, unknown> | undefined;
  if (!existing) {
    return c.json({ error: 'Not found' }, 404);
  }

  db.prepare('DELETE FROM todos WHERE id = ? AND user_id = ?').run(id, userId);
  return c.json({ success: true }, 200);
});

/** @internal Phoenix VCS traceability — do not remove. */
/** @internal Phoenix VCS traceability — do not remove. */


export default router;

/** @internal Phoenix VCS traceability — do not remove. */
export const _phoenix = {
  iu_id: 'b1ce9f3f792107a5e24af89c09b255c96c14336fa5511b8591df70ad47b2140a',
  name: 'Update Todo',
  risk_tier: 'low',
  canon_ids: [2 as const],
} as const;
