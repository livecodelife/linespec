# Todo API

A JSON REST API for managing personal todo items. Users authenticate via a bearer
token that is verified against an external User Service. All todo operations are
scoped to the authenticated user.

## Authentication

Every request must include an `Authorization: Bearer <token>` header. The server
verifies the token by calling `GET` on the User Service auth endpoint, passing
the same `Authorization` header. If the User Service returns 200 the request
proceeds; any other status returns 401 to the caller.

The full auth endpoint URL is provided in `process.env.USER_SERVICE_URL` (exact
casing — uppercase with underscores). This is the complete URL including path —
do NOT append any path to it. The fetch call must be exactly:
`fetch(process.env.USER_SERVICE_URL!, { headers: { Authorization: authHeader } })`.
Do not append `/api/v1/users/auth` or any other path segment to `USER_SERVICE_URL`.

The authenticated user's `id` is extracted from the User Service response and
used to scope all database queries.

Error responses use the exact casing `"Unauthorized"` (capital U) for 401 errors.

## List Todos

`GET /api/v1/todos` returns all todos belonging to the authenticated user, ordered
by `created_at` descending. The response body is a JSON array. An empty array is
returned when the user has no todos. The HTTP status is always 200.

## Create Todo

`POST /api/v1/todos` creates a new todo for the authenticated user.

Request body fields:
- `title` (string, required) — the todo's title; must not be blank
- `description` (string, optional) — extended notes
- `completed` (boolean, optional, default false)

On success the server returns 201 with the created todo including its generated
`id`, `user_id`, `created_at`, and `updated_at`. If `title` is missing or blank
the server returns 400 with `{ "error": "title is required" }`.

## Get Todo

`GET /api/v1/todos/:id` returns the todo with the given id. The todo must belong
to the authenticated user; if it does not exist or belongs to another user the
server returns 404 with `{ "error": "not found" }`.

## Update Todo

`PATCH /api/v1/todos/:id` updates the todo. Accepts the same optional fields as
Create. Only provided fields are updated; omitted fields are left unchanged.
Returns 200 with the updated todo. Returns 404 if not found or not owned by the
user.

## Delete Todo

`DELETE /api/v1/todos/:id` deletes the todo. Returns 204 with no body. Returns
404 if not found or not owned by the user.
