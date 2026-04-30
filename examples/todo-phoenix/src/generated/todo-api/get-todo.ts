import { Hono } from 'hono';
import { db } from '../../db.js';
import { authenticate } from '../../auth.js';

const router = new Hono();

router.get('/:id', async (c) => {
  const user = await authenticate(c.req.header('Authorization'));
  if (!user) return c.json({ error: 'Unauthorized' }, 401);

  const id = parseInt(c.req.param('id'), 10);
  if (isNaN(id)) return c.json({ error: 'not found' }, 404);

  const todo = db.prepare('SELECT * FROM todos WHERE id = ? AND user_id = ?').get(id, user.id);
  if (!todo) return c.json({ error: 'not found' }, 404);

  return c.json(todo);
});

export default router;
