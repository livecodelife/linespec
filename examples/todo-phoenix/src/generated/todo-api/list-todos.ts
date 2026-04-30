import { Hono } from 'hono';
import { db } from '../../db.js';
import { authenticate } from '../../auth.js';

const router = new Hono();

router.get('/', async (c) => {
  const user = await authenticate(c.req.header('Authorization'));
  if (!user) return c.json({ error: 'Unauthorized' }, 401);
  const todos = db.prepare('SELECT * FROM todos WHERE user_id = ?').all(user.id);
  return c.json(todos);
});

export default router;

/** @internal Phoenix VCS traceability — do not remove. */
export const _phoenix = {
  iu_id: 'aa8b81c228a0b071ae381335360696b9ec7c39c154285603a6085fd4fd36b8ec',
  name: 'List Todos',
  risk_tier: 'high',
  canon_ids: [2 as const],
} as const;
