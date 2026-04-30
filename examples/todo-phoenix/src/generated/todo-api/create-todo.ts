import { Hono } from 'hono';
import { db } from '../../db.js';
import { authenticate } from '../../auth.js';

const router = new Hono();

router.post('/', async (c) => {
  const user = await authenticate(c.req.header('Authorization'));
  if (!user) return c.json({ error: 'Unauthorized' }, 401);

  const body = await c.req.json().catch(() => ({})) as Record<string, unknown>;
  if (!body.title || String(body.title).trim() === '') {
    return c.json({ error: 'title is required' }, 400);
  }

  const result = db.prepare(
    'INSERT INTO todos (user_id, title, description, completed) VALUES (?, ?, ?, 0)',
  ).run(user.id, String(body.title).trim(), body.description ?? null);

  const todo = db.prepare('SELECT * FROM todos WHERE id = ?').get(result.lastInsertRowid);
  return c.json(todo, 201);
});

export default router;

/** @internal Phoenix VCS traceability — do not remove. */
export const _phoenix = {
  iu_id: 'e3eddeb7a82aad689d0fefd0f28a3162d65312f9528d35d8782395fae55f5c00',
  name: 'Create Todo',
  risk_tier: 'high',
  canon_ids: [5 as const],
} as const;
