import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { AlertTriangle, CheckCircle2, ExternalLink, Loader2, RefreshCw, RotateCw, Search, Trash2 } from 'lucide-react'
import {
  deleteFailedTracker,
  fetchTrackers,
  recheckTracker,
  UnauthorizedError,
  type TrackersResponse,
  type Credentials,
  type FailedTracker,
} from '@/api'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { formatDate } from '@/lib/utils'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

// The worker picks due trackers up on a 30s tick and may then walk the whole fetch chain
// (up to a few minutes for a heavily protected site), so a recheck is watched for a while.
const RECHECK_POLL_MS = 5_000
const RECHECK_POLL_ATTEMPTS = 48

type RecheckState =
  | { phase: 'queued' }
  | { phase: 'failed' }
  | { phase: 'waiting' }
  | { phase: 'error'; message: string }

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

export function TrackersPage({
  credentials,
  onAuthFailure,
}: {
  credentials: Credentials
  onAuthFailure: (err: unknown) => void
}) {
  const [data, setData] = useState<TrackersResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [deleteError, setDeleteError] = useState<string | null>(null)
  const [deletingID, setDeletingID] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  const [failureFilter, setFailureFilter] = useState<'all' | 'repeated' | 'setup'>('all')
  const [rechecks, setRechecks] = useState<Record<string, RecheckState>>({})
  const [recovered, setRecovered] = useState<string[]>([])
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])

  const load = useCallback(() => {
    setError(null)
    fetchTrackers(credentials)
      .then(setData)
      .catch((err) => {
        setError(err.message)
        onAuthFailure(err)
      })
  }, [credentials, onAuthFailure])

  useEffect(() => {
    load()
  }, [load])

  const filteredFailures = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return (data?.failedTrackers ?? []).filter((item) => {
      if (failureFilter === 'repeated' && item.consecutiveErrors < 3) return false
      if (failureFilter === 'setup' && item.kind !== 'extraction_failure') return false
      return !needle || [item.title, item.url, item.userName, item.error].some((value) =>
        value.toLowerCase().includes(needle),
      )
    })
  }, [data, failureFilter, query])

  async function handleDeleteFailedTracker(item: FailedTracker) {
    if (!window.confirm('Remove this item from failed extraction list?')) return
    setDeletingID(item.id)
    setDeleteError(null)
    try {
      await deleteFailedTracker(credentials, item)
      setData((current) =>
        current
          ? {
              ...current,
              failedTrackers: current.failedTrackers.filter(
                (failed) => failed.id !== item.id || failed.kind !== item.kind,
              ),
            }
          : current,
      )
    } catch (err) {
      if (err instanceof UnauthorizedError) {
        onAuthFailure(err)
        return
      }
      setDeleteError(err instanceof Error ? err.message : 'Failed to delete item')
    } finally {
      setDeletingID(null)
    }
  }

  function setRecheck(id: string, state: RecheckState | null) {
    setRechecks((current) => {
      const next = { ...current }
      if (state) next[id] = state
      else delete next[id]
      return next
    })
  }

  // Queue the tracker, then watch the problem list until the worker has run it: gone from
  // the list means it recovered, a new last-checked time means it failed again (the row
  // then shows the fresh error).
  async function handleRecheck(item: FailedTracker) {
    setRecheck(item.id, { phase: 'queued' })
    let since: string
    try {
      since = (await recheckTracker(credentials, item.id)).lastCheckedAt
    } catch (err) {
      if (err instanceof UnauthorizedError) {
        onAuthFailure(err)
        return
      }
      setRecheck(item.id, { phase: 'error', message: err instanceof Error ? err.message : 'Failed to queue recheck' })
      return
    }

    for (let attempt = 0; attempt < RECHECK_POLL_ATTEMPTS; attempt++) {
      await sleep(RECHECK_POLL_MS)
      if (!mounted.current) return
      let fresh: TrackersResponse
      try {
        fresh = await fetchTrackers(credentials)
      } catch {
        continue
      }
      if (!mounted.current) return
      const row = fresh.failedTrackers.find((f) => f.kind === 'tracker_error' && f.id === item.id)
      if (!row) {
        setData(fresh)
        setRecheck(item.id, null)
        setRecovered((current) => [...current, item.title])
        return
      }
      if ((row.timestamp ?? '') !== since) {
        setData(fresh)
        setRecheck(item.id, { phase: 'failed' })
        return
      }
    }
    setRecheck(item.id, { phase: 'waiting' })
  }

  if (error) return <p className="text-destructive">Failed to load: {error}</p>
  if (!data) return <p className="text-muted-foreground">Loading…</p>

  return (
    <div className="flex flex-col gap-6">
      <Card>
        <CardHeader>
          <CardTitle>Resolved via a search fallback ({data.fallbackTrackers.length})</CardTitle>
          <CardDescription>
            Trackers whose price came from a search service instead of reading the page
            directly.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>ID</TableHead>
                <TableHead>User</TableHead>
                <TableHead>Product</TableHead>
                <TableHead>Fallback</TableHead>
                <TableHead>Last checked</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.fallbackTrackers.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={5} className="text-muted-foreground">
                    None currently.
                  </TableCell>
                </TableRow>
              ) : (
                data.fallbackTrackers.map((t, i) => (
                  <TableRow key={`${t.userId}-${t.url}-${i}`}>
                    <TableCell className="font-mono text-xs text-muted-foreground">{t.id}</TableCell>
                    <TableCell>{t.userName}</TableCell>
                    <TableCell>
                      <a
                        href={t.url}
                        target="_blank"
                        rel="noreferrer"
                        className="text-primary underline-offset-4 hover:underline"
                      >
                        {t.title}
                      </a>
                    </TableCell>
                    <TableCell>
                      <Badge variant="outline">{t.method}</Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground">{formatDate(t.timestamp)}</TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div>
              <CardTitle className="flex items-center gap-2">
                <AlertTriangle className="size-5 text-destructive" />
                Problem trackers ({data.failedTrackers.length})
              </CardTitle>
              <CardDescription className="mt-1">
                Repeated failures are shown first. Setup failures happened before a tracker was created.
              </CardDescription>
            </div>
            <Button type="button" variant="outline" size="sm" onClick={load}>
              <RefreshCw /> Refresh
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          {deleteError ? (
            <p className="mb-3 text-sm text-destructive">{deleteError}</p>
          ) : null}
          {recovered.map((title, i) => (
            <p key={`${title}-${i}`} className="mb-2 flex items-center gap-1.5 text-sm text-green-700 dark:text-green-400">
              <CheckCircle2 className="size-4 shrink-0" />
              Recovered after recheck: {title}
            </p>
          ))}
          <div className="mb-4 flex flex-col gap-3 sm:flex-row sm:items-center">
            <div className="relative min-w-0 flex-1">
              <Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="Search product, user, URL or error…"
                className="pl-9"
              />
            </div>
            <div className="flex gap-2">
              {(['all', 'repeated', 'setup'] as const).map((filter) => (
                <Button
                  key={filter}
                  type="button"
                  size="sm"
                  variant={failureFilter === filter ? 'default' : 'outline'}
                  onClick={() => setFailureFilter(filter)}
                >
                  {filter === 'all' ? 'All' : filter === 'repeated' ? 'Repeated' : 'Setup'}
                </Button>
              ))}
            </div>
          </div>
          <div className="overflow-x-auto rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>ID</TableHead>
                <TableHead>User</TableHead>
                <TableHead>Product</TableHead>
                <TableHead>Error</TableHead>
                <TableHead>Severity</TableHead>
                <TableHead>Last checked</TableHead>
                <TableHead className="w-20">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {filteredFailures.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={7} className="h-24 text-center text-muted-foreground">
                    {data.failedTrackers.length === 0 ? 'No failing trackers right now.' : 'No errors match these filters.'}
                  </TableCell>
                </TableRow>
              ) : (
                filteredFailures.map((t) => (
                  <TableRow key={`${t.kind}-${t.id}`} className={t.consecutiveErrors >= 3 ? 'bg-destructive/5' : undefined}>
                    <TableCell className="font-mono text-xs text-muted-foreground">{t.id}</TableCell>
                    <TableCell>{t.userName}</TableCell>
                    <TableCell>
                      <div className="flex max-w-[28rem] flex-col gap-1 whitespace-normal">
                        <a
                          href={t.url}
                          target="_blank"
                          rel="noreferrer"
                          className="text-primary underline-offset-4 hover:underline"
                        >
                          {t.title}
                          <ExternalLink className="ml-1 inline size-3" />
                        </a>
                        <Badge variant="outline" className="w-fit">
                          {t.kind === 'tracker_error' ? 'tracker' : 'failure log'}
                        </Badge>
                      </div>
                    </TableCell>
                    <TableCell className="max-w-[32rem] whitespace-normal text-destructive">
                      <span className="line-clamp-3 font-mono text-xs" title={t.error}>{t.error}</span>
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-col items-start gap-1">
                        <Badge variant={t.consecutiveErrors >= 3 ? 'destructive' : 'outline'}>
                          {t.kind === 'extraction_failure'
                            ? 'setup failed'
                            : `${t.consecutiveErrors}× consecutive`}
                        </Badge>
                        <RecheckStatus state={rechecks[t.id]} />
                      </div>
                    </TableCell>
                    <TableCell className="text-muted-foreground">{formatDate(t.timestamp)}</TableCell>
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-1">
                        {t.kind === 'tracker_error' ? (
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon-sm"
                            aria-label="Recheck tracker now"
                            title="Recheck now"
                            disabled={rechecks[t.id]?.phase === 'queued'}
                            onClick={() => void handleRecheck(t)}
                          >
                            {rechecks[t.id]?.phase === 'queued' ? (
                              <Loader2 className="animate-spin" />
                            ) : (
                              <RotateCw />
                            )}
                          </Button>
                        ) : null}
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-sm"
                          aria-label="Remove failed extraction item"
                          disabled={deletingID === t.id}
                          onClick={() => void handleDeleteFailedTracker(t)}
                        >
                          {deletingID === t.id ? (
                            <Loader2 className="animate-spin" />
                          ) : (
                            <Trash2 />
                          )}
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
          </div>
        </CardContent>
      </Card>

      <p className="text-xs text-muted-foreground">Generated {formatDate(data.generatedAt)}</p>
    </div>
  )
}

function RecheckStatus({ state }: { state?: RecheckState }) {
  if (!state) return null
  switch (state.phase) {
    case 'queued':
      return <span className="text-xs text-muted-foreground">Recheck queued…</span>
    case 'failed':
      return <Badge variant="destructive">Recheck failed</Badge>
    case 'waiting':
      return <span className="text-xs text-muted-foreground">Not checked yet — refresh later</span>
    case 'error':
      return <span className="max-w-[12rem] whitespace-normal text-xs text-destructive">{state.message}</span>
  }
}
