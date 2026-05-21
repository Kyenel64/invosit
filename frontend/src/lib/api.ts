
const API_BASE = "/api/v1";

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message);
    this.name = "ApiError";
  }
}

export class UnauthorizedError extends ApiError {
  constructor(code: string, message: string) {
    super(401, code, message);
    this.name = "UnauthorizedError";
  }
}

interface ErrorEnvelope {
  error?: string;
  code?: string;
}

async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const hasBody = init?.body !== undefined && init.body !== null;
  const res = await fetch(`${API_BASE}${path}`, {
    credentials: "include",
    ...init,
    headers: {
      Accept: "application/json",
      ...(hasBody ? { "Content-Type": "application/json" } : {}),
      ...init?.headers,
    },
  });

  if (!res.ok) {
    const body: ErrorEnvelope = await res.json().catch(() => ({}));
    const code = body.code ?? "UNKNOWN";
    const message = body.error ?? `request failed: ${res.status}`;
    if (res.status === 401) throw new UnauthorizedError(code, message);
    throw new ApiError(res.status, code, message);
  }

  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export interface User {
  id: string;
  email: string;
  created_at: string;
}

export const api = {
  me: () => apiFetch<User>("/auth/me"),
};
