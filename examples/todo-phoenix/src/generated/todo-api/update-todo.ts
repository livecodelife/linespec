import { Hono } from 'hono';
import { db } from '../../db.js';
import { authenticate } from '../../auth.js';
import type { Todo } from '../../types.js';

const router = new Hono();

router.patch('/:id', async (c) => {
  const user = await authenticate(c.req.header('Authorization'));
  if (!user) return c.json({ error: 'Unauthorized' }, 401);

  const id = parseInt(c.req.param('id'), 10);
  if (isNaN(id)) return c.json({ error: 'not found' }, 404);

  const existing = db.prepare('SELECT * FROM todos WHERE id = ? AND user_id = ?').get(id, user.id) as Todo | undefined;
  if (!existing) return c.json({ error: 'not found' }, 404);

  const body = await c.req.json().catch(() => ({})) as Record<string, unknown>;
  const title = body.title !== undefined ? String(body.title).trim() : existing.title;
  const description = body.description !== undefined ? body.description : existing.description;
  const completed = body.completed !== undefined ? (body.completed ? 1 : 0) : existing.completed;

  db.prepare(
    "UPDATE todos SET title = ?, description = ?, completed = ?, updated_at = datetime('now') WHERE id = ?",
  ).run(title, description, completed, id);

  const todo = db.prepare('SELECT * FROM todos WHERE id = ?').get(id);
  return c.json(todo);
});

export default router;

/** @internal Phoenix VCS traceability — do not remove. */
export const _phoenix = {
  iu_id: 'b1ce9f3f792107a5e24af89c09b255c96c14336fa5511b8591df70ad47b2140a',
  name: 'Update Todo',
  risk_tier: 'low',
  canon_ids: [2 as const],
} as const;
