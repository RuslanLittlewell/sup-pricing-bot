export interface AdminUser {
  id: string
  name: string
  email: string
  planCode: string
  trackerCount: number
}

export interface FallbackTracker {
  userId: string
  userName: string
  title: string
  url: string
  method: string
  timestamp: string
}

export interface FailedTracker {
  id: string
  kind: 'tracker_error' | 'extraction_failure'
  userId: string
  userName: string
  title: string
  url: string
  error: string
  timestamp: string
}

export interface TrackersResponse {
  fallbackTrackers: FallbackTracker[]
  failedTrackers: FailedTracker[]
  generatedAt: string
}

// The deployed API (backend/cmd/api) — the /api/admin/* routes are reachable directly
// over HTTPS, no SSH tunnel needed. Override with VITE_API_URL for local testing against
// a backend run on your own machine.
const API_URL = import.meta.env.VITE_API_URL ?? 'https://pricebot-api.littlewell-app.work'

const CREDENTIALS_KEY = 'admin_credentials'

export interface Credentials {
  username: string
  password: string
}

export function loadStoredCredentials(): Credentials | null {
  const raw = localStorage.getItem(CREDENTIALS_KEY)
  if (!raw) return null
  try {
    return JSON.parse(raw)
  } catch {
    return null
  }
}

export function storeCredentials(creds: Credentials) {
  localStorage.setItem(CREDENTIALS_KEY, JSON.stringify(creds))
}

export function clearCredentials() {
  localStorage.removeItem(CREDENTIALS_KEY)
}

// UnauthorizedError signals the stored credentials were rejected, so the caller can drop
// them and show the login form again instead of retrying forever.
export class UnauthorizedError extends Error {}

async function adminGet<T>(path: string, creds: Credentials): Promise<T> {
  const res = await fetch(`${API_URL}${path}`, {
    headers: {
      Authorization: 'Basic ' + btoa(`${creds.username}:${creds.password}`),
    },
  })
  if (res.status === 401) {
    throw new UnauthorizedError('Invalid username or password')
  }
  if (!res.ok) {
    throw new Error(`GET ${path} failed: ${res.status}`)
  }
  return res.json()
}

async function adminDelete(path: string, creds: Credentials): Promise<void> {
  const res = await fetch(`${API_URL}${path}`, {
    method: 'DELETE',
    headers: {
      Authorization: 'Basic ' + btoa(`${creds.username}:${creds.password}`),
    },
  })
  if (res.status === 401) {
    throw new UnauthorizedError('Invalid username or password')
  }
  if (!res.ok) {
    throw new Error(`DELETE ${path} failed: ${res.status}`)
  }
}

export function fetchUsers(creds: Credentials): Promise<AdminUser[]> {
  return adminGet('/api/admin/users', creds)
}

export function fetchTrackers(creds: Credentials): Promise<TrackersResponse> {
  return adminGet('/api/admin/trackers', creds)
}

export function deleteFailedTracker(
  creds: Credentials,
  item: Pick<FailedTracker, 'id' | 'kind'>,
): Promise<void> {
  return adminDelete(
    `/api/admin/trackers/failed/${encodeURIComponent(item.kind)}/${encodeURIComponent(item.id)}`,
    creds,
  )
}
