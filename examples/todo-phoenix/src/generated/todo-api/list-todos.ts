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

router.get('/api/v1/todos', async (c) => {
  const authHeader = c.req.header('Authorization');
  if (!authHeader) {
    return c.json({ error: 'Unauthorized' }, 401);
  }

  const userServiceUrl = process.env.USER_SERVICE_URL;
  if (!userServiceUrl) {
    return c.json({ error: 'Unauthorized' }, 401);
  }

  try {
    const response = await fetch(userServiceUrl, {
      headers: { Authorization: authHeader },
    });

    if (response.status !== 200) {
      return c.json({ error: 'Unauthorized' }, 401);
    }

    const userData = await response.json() as { id: string };
    const userId = userData.id;

    const todos = db.prepare(
      "SELECT id, user_id, title, description, completed, created_at, updated_at FROM todos WHERE user_id = ? ORDER BY created_at DESC"
    ).all(userId);

    return c.json(todos, 200);
  } catch {
    return c.json({ error: 'Unauthorized' }, 401);
  }
});

router.delete('/api/v1/todos/:id', async (c) => {
  const authHeader = c.req.header('Authorization');
  if (!authHeader) {
    return c.json({ error: 'Unauthorized' }, 401);
  }

  const userServiceUrl = process.env.USER_SERVICE_URL;
  if (!userServiceUrl) {
    return c.json({ error: 'Unauthorized' }, 401);
  }

  try {
    const response = await fetch(userServiceUrl, {
      headers: { Authorization: authHeader },
    });

    if (response.status !== 200) {
      return c.json({ error: 'Unauthorized' }, 401);
    }

    const userData = await response.json() as { id: string };
    const userId = userData.id;
    const id = Number(c.req.param('id'));

    const todo = db.prepare("SELECT * FROM todos WHERE id = ?").get(id) as Record<string, unknown> | undefined;

    if (!todo) {
      return c.json({ error: 'Not found' }, 404);
    }

    if (todo.user_id !== userId) {
      return c.json({ error: 'Unauthorized' }, 401);
    }

    db.prepare("DELETE FROM todos WHERE id = ?").run(id);

    return new Response(null, { status: 204 });
  } catch {
    return c.json({ error: 'Unauthorized' }, 401);
  }
});

router.patch('/api/v1/todos/:id', async (c) => {
  const authHeader = c.req.header('Authorization');
  if (!authHeader) {
    return c.json({ error: 'Unauthorized' }, 401);
  }

  const userServiceUrl = process.env.USER_SERVICE_URL;
  if (!userServiceUrl) {
    return c.json({ error: 'Unauthorized' }, 401);
  }

  try {
    const response = await fetch(userServiceUrl, {
      headers: { Authorization: authHeader },
    });

    if (response.status !== 200) {
      return c.json({ error: 'Unauthorized' }, 401);
    }

    const userData = await response.json() as { id: string };
    const userId = userData.id;
    const id = Number(c.req.param('id'));

    const todo = db.prepare("SELECT * FROM todos WHERE id = ?").get(id) as Record<string, unknown> | undefined;

    if (!todo) {
      return c.json({ error: 'Not found' }, 404);
    }

    if (todo.user_id !== userId) {
      return c.json({ error: 'Unauthorized' }, 401);
    }

    const body = await c.req.json();
    const parsed = UpdateTodoSchema.safeParse(body);

    if (!parsed.success) {
      return c.json({ error: 'Bad request' }, 400);
    }

    const updates: Record<string, unknown> = {};
    const values: unknown[] = [];

    if (parsed.data.title !== undefined) {
      updates['title'] = parsed.data.title;
    }
    if (parsed.data.description !== undefined) {
      updates['description'] = parsed.data.description;
    }
    if (parsed.data.completed !== undefined) {
      updates['completed'] = parsed.data.completed ? 1 : 0;
    }

    if (Object.keys(updates).length > 0) {
      updates['updated_at'] = new Date().toISOString();
      const setClauses = Object.keys(updates).map(k => `${k} = ?`).join(', ');
      values.push(...Object.values(updates));
      values.push(id);

      db.prepare(`UPDATE todos SET ${setClauses} WHERE id = ?`).run(...values);
    }

    const updatedTodo = db.prepare("SELECT id, user_id, title, description, completed, created_at, updated_at FROM todos WHERE id = ?").get(id);

    return c.json(updatedTodo, 200);
  } catch {
    return c.json({ error: 'Unauthorized' }, 401);
  }
});

/** @internal Phoenix VCS traceability — do not remove. */
/** @internal Phoenix VCS traceability — do not remove. */
/** @internal Phoenix VCS traceability — do not remove. */


export default router;

/** @internal Phoenix VCS traceability — do not remove. */
export const _phoenix = {
  iu_id: 'aa8b81c228a0b071ae381335360696b9ec7c39c154285603a6085fd4fd36b8ec',
  name: 'List Todos',
  risk_tier: 'high',
  canon_ids: [2 as const],
} as const;
