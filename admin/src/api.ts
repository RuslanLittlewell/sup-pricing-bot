export interface AdminUser {
  id: string
  name: string
  email: string
  planCode: string
  trackerCount: number
}

export interface UserTracker {
  id: string
  title: string
  url: string
  domain: string
  status: string
  initialPrice: number
  currentPrice: number | null
  currency: string
  extractionMethod: string
  latestExtractionMethod?: string
  latestFetchMethod?: string
  createdAt: string
  lastCheckedAt: string | null
  consecutiveErrors: number
  lastError: string | null
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
  consecutiveErrors: number
}

export interface TrackersResponse {
  fallbackTrackers: FallbackTracker[]
  failedTrackers: FailedTracker[]
  generatedAt: string
}

export interface ServiceStatus {
  status: 'healthy' | 'degraded'
  databaseStatus: string
  workerStatus: string
  workerLastSeenAt: string | null
  workerAgeSeconds: number | null
  activeTrackers: number
  failingTrackers: number
  dueTrackers: number
  pendingNotifications: number
  databaseConnections: number
  checkedAt: string
}

export interface AdminProxy {
  id: string
  address: string
  protocol: string
  countryCode: string
  status: string
  source: string
  networkType: 'residential' | 'mobile' | 'datacenter' | 'unknown'
  provider: string
  asn: string
  hasAuth: boolean
  useCount: number
  lastCheckedAt: string | null
  lastUsedAt: string | null
  createdAt: string
}

export interface PlaygroundResult {
  tool: string
  url: string
  durationMs: number
  fetchMethod?: string
  bodyBytes?: number
  stockStatus?: string
  result?: unknown
  error?: string
}

// The deployed API (backend/cmd/api) — the /api/admin/* routes are reachable directly
// over HTTPS, no SSH tunnel needed. Override with VITE_API_URL for local testing against
// a backend run on your own machine.
const API_URL = import.meta.env.VITE_API_URL ?? 'https://pricebot-api.surpricebot.com'

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

export function fetchServiceStatus(creds: Credentials): Promise<ServiceStatus> {
  return adminGet('/api/admin/status', creds)
}

export function fetchUserTrackers(creds: Credentials, userId: string): Promise<UserTracker[]> {
  return adminGet(`/api/admin/users/${encodeURIComponent(userId)}/trackers`, creds)
}

export function fetchProxies(creds: Credentials): Promise<AdminProxy[]> {
  return adminGet('/api/admin/proxies', creds)
}

export async function runPlaygroundTool(creds: Credentials, tool: string, url: string): Promise<PlaygroundResult> {
  const res = await fetch(`${API_URL}/api/admin/playground/run`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: 'Basic ' + btoa(`${creds.username}:${creds.password}`),
    },
    body: JSON.stringify({ tool, url }),
  })
  if (res.status === 401) throw new UnauthorizedError('Invalid username or password')
  if (!res.ok) throw new Error(`Playground request failed: ${res.status}`)
  return res.json()
}

export async function addProxies(
  creds: Credentials,
  text: string,
  metadata: { networkType: AdminProxy['networkType']; provider: string; asn: string; countryCode: string },
): Promise<{ added: number; invalid: string[] }> {
  const res = await fetch(`${API_URL}/api/admin/proxies`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: 'Basic ' + btoa(`${creds.username}:${creds.password}`),
    },
    body: JSON.stringify({ text, ...metadata }),
  })
  if (res.status === 401) {
    throw new UnauthorizedError('Invalid username or password')
  }
  if (!res.ok) {
    let msg = `POST /api/admin/proxies failed: ${res.status}`
    try {
      const body = (await res.json()) as { error?: string }
      if (body.error) msg = body.error
    } catch {
      // response had no JSON error body; keep the generic status message
    }
    throw new Error(msg)
  }
  // The backend marshals a nil Go slice as null when every line parsed.
  const body = (await res.json()) as { added: number; invalid: string[] | null }
  return { added: body.added, invalid: body.invalid ?? [] }
}

export async function checkProxy(
  creds: Credentials,
  id: string,
): Promise<{ address: string; alive: boolean }> {
  const res = await fetch(`${API_URL}/api/admin/proxies/${encodeURIComponent(id)}/check`, {
    method: 'POST',
    headers: {
      Authorization: 'Basic ' + btoa(`${creds.username}:${creds.password}`),
    },
  })
  if (res.status === 401) {
    throw new UnauthorizedError('Invalid username or password')
  }
  if (!res.ok) {
    throw new Error(`POST /api/admin/proxies/${id}/check failed: ${res.status}`)
  }
  return res.json()
}

export async function recheckDeadProxies(
  creds: Credentials,
): Promise<{ checked: number; alive: number }> {
  const res = await fetch(`${API_URL}/api/admin/proxies/recheck`, {
    method: 'POST',
    headers: {
      Authorization: 'Basic ' + btoa(`${creds.username}:${creds.password}`),
    },
  })
  if (res.status === 401) {
    throw new UnauthorizedError('Invalid username or password')
  }
  if (!res.ok) {
    throw new Error(`POST /api/admin/proxies/recheck failed: ${res.status}`)
  }
  return res.json()
}

export async function deleteDeadProxies(creds: Credentials): Promise<{ removed: number }> {
  const res = await fetch(`${API_URL}/api/admin/proxies/dead`, {
    method: 'DELETE',
    headers: {
      Authorization: 'Basic ' + btoa(`${creds.username}:${creds.password}`),
    },
  })
  if (res.status === 401) {
    throw new UnauthorizedError('Invalid username or password')
  }
  if (!res.ok) {
    throw new Error(`DELETE /api/admin/proxies/dead failed: ${res.status}`)
  }
  return res.json()
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
