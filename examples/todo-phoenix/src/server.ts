import { serve } from '@hono/node-server';
import { app, mount } from './app.js';
import { runMigrations } from './db.js';

import list_todos from './generated/todo-api/list-todos.js';
import create_todo from './generated/todo-api/create-todo.js';
import get_todo from './generated/todo-api/get-todo.js';
import update_todo from './generated/todo-api/update-todo.js';
import delete_todo from './generated/todo-api/delete-todo.js';

mount('/api/v1/todos', list_todos);
mount('/api/v1/todos', create_todo);
mount('/api/v1/todos', get_todo);
mount('/api/v1/todos', update_todo);
mount('/api/v1/todos', delete_todo);

const port = parseInt(process.env.PORT ?? '3000', 10);
runMigrations();
console.log(`Server running at http://localhost:${port}`);
serve({ fetch: app.fetch, port });
