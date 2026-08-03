import { useCallback, useEffect, useMemo, useState } from 'react'
import { AlertTriangle, ExternalLink, Loader2, RefreshCw, Search, Trash2 } from 'lucide-react'
import {
  deleteFailedTracker,
  fetchTrackers,
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
                <TableHead className="w-10">
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
                      <Badge variant={t.consecutiveErrors >= 3 ? 'destructive' : 'outline'}>
                        {t.kind === 'extraction_failure'
                          ? 'setup failed'
                          : `${t.consecutiveErrors}× consecutive`}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground">{formatDate(t.timestamp)}</TableCell>
                    <TableCell className="text-right">
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
