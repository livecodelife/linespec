import { Hono } from 'hono';
import { db, registerMigration } from '../../db.js';
import { z } from 'zod';

// ─── Database migrations ────────────────────────────────────────────────────

// ─── Database migrations ────────────────────────────────────────────────────
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

const router = new Hono<{ Variables: { userId: string } }>();

const CreateTodoSchema = z.object({
  title: z.string().min(1),
  description: z.string().optional(),
  completed: z.boolean().optional().default(false),
});

const UpdateTodoSchema = z.object({
  title: z.string().min(1).optional(),
  description: z.string().optional(),
  completed: z.boolean().optional(),
});

// Auth middleware
router.use('*', async (c, next) => {
  const authHeader = c.req.header('Authorization');
  if (!authHeader) {
    return c.json({ error: 'Unauthorized' }, 401);
  }

  const userServiceUrl = process.env.USER_SERVICE_URL;
  if (!userServiceUrl) {
    return c.json({ error: 'Unauthorized' }, 401);
  }

  try {
    const res = await fetch(userServiceUrl, {
      headers: { Authorization: authHeader },
    });

    if (res.status !== 200) {
      return c.json({ error: 'Unauthorized' }, 401);
    }

    const userData = (await res.json()) as { id: number };
    c.set('userId', String(userData.id));
  } catch {
    return c.json({ error: 'Unauthorized' }, 401);
  }

  await next();
});

router.delete('/:id', async (c) => {
  const userId = c.get('userId');
  const id = Number(c.req.param('id'));

  const todo = db
    .prepare('SELECT id FROM todos WHERE id = ? AND user_id = ?')
    .get(id, userId) as { id: number } | undefined;

  if (!todo) {
    return c.json({ error: 'Not found' }, 404);
  }

  db.prepare('DELETE FROM todos WHERE id = ? AND user_id = ?').run(id, userId);

  return c.body(null, 204);
});

/** @internal Phoenix VCS traceability — do not remove. */
/** @internal Phoenix VCS traceability — do not remove. */


export default router;

/** @internal Phoenix VCS traceability — do not remove. */
export const _phoenix = {
  iu_id: '4f92a10b1dfd8ab355ee132d638d0f7f049ab701465f540df62eca41b5f3b4cf',
  name: 'Delete Todo',
  risk_tier: 'low',
  canon_ids: [2 as const],
} as const;
