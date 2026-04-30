export interface AuthUser {
  id: number;
  email: string;
  name: string;
}

export interface Todo {
  id: number;
  user_id: number;
  title: string;
  description: string | null;
  completed: number;
  created_at: string;
  updated_at: string;
}
