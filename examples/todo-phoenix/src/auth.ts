import type { AuthUser } from './types.js';

export type { AuthUser };

export async function authenticate(authHeader: string | undefined): Promise<AuthUser | null> {
  if (!authHeader?.startsWith('Bearer ')) return null;
  const url = process.env.USER_SERVICE_URL ?? 'http://user-service:3001/api/v1/users/auth';
  const res = await fetch(url, { headers: { Authorization: authHeader } });
  if (!res.ok) return null;
  return res.json() as Promise<AuthUser>;
}
