import { authClient } from "./auth-client";

export interface Tracker {
  id: string;
  url: string;
  domain: string;
  title: string | null;
  current_price: number | null;
  currency: string;
  current_stock_status: string;
  status: string;
  last_checked_at: string | null;
}

export interface TrackerHistory {
  prices: { price: number; currency: string; checked_at: string }[];
  stocks: { stock_status: string; checked_at: string }[];
}

const API_URL = import.meta.env.PUBLIC_API_URL || import.meta.env.API_URL || "";
let cachedToken: string | null = null;

export class ApiError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

export function isAuthError(error: unknown) {
  if (error instanceof ApiError) {
    return error.status === 401 || error.status === 403;
  }

  const message = String(error).toLowerCase();
  return message.includes("no session") || message.includes("invalid session");
}

export async function getAuthToken(): Promise<string | null> {
  if (cachedToken) return cachedToken;
  try {
    const session = await authClient.getSession();
    cachedToken = session.data?.session?.token || null;
    return cachedToken;
  } catch {
    return null;
  }
}

export async function api(path: string, options: RequestInit = {}) {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    ...(options.headers as Record<string, string>),
  };

  const token = await getAuthToken();
  if (token) {
    headers["X-Session-Token"] = token;
  }

  const res = await fetch(`${API_URL}${path}`, {
    credentials: "include",
    headers,
    ...options,
  });
  if (!res.ok) {
    const text = await res.text();
    throw new ApiError(res.status, text || res.statusText);
  }
  return res.json();
}
