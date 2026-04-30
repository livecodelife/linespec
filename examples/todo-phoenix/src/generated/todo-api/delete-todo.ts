import { Hono } from 'hono';
import { db } from '../../db.js';
import { authenticate } from '../../auth.js';

const router = new Hono();

router.delete('/:id', async (c) => {
  const user = await authenticate(c.req.header('Authorization'));
  if (!user) return c.json({ error: 'Unauthorized' }, 401);

  const id = parseInt(c.req.param('id'), 10);
  if (isNaN(id)) return c.json({ error: 'not found' }, 404);

  const existing = db.prepare('SELECT * FROM todos WHERE id = ? AND user_id = ?').get(id, user.id);
  if (!existing) return c.json({ error: 'not found' }, 404);

  db.prepare('DELETE FROM todos WHERE id = ?').run(id);
  return c.newResponse(null, 204);
});

export default router;

/** @internal Phoenix VCS traceability — do not remove. */
export const _phoenix = {
  iu_id: '4f92a10b1dfd8ab355ee132d638d0f7f049ab701465f540df62eca41b5f3b4cf',
  name: 'Delete Todo',
  risk_tier: 'low',
  canon_ids: [2 as const],
} as const;
