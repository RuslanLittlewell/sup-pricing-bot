import { useCallback, useEffect, useState } from 'react'
import { Loader2, Trash2 } from 'lucide-react'
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
                <TableHead>User</TableHead>
                <TableHead>Product</TableHead>
                <TableHead>Fallback</TableHead>
                <TableHead>Last checked</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.fallbackTrackers.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={4} className="text-muted-foreground">
                    None currently.
                  </TableCell>
                </TableRow>
              ) : (
                data.fallbackTrackers.map((t, i) => (
                  <TableRow key={`${t.userId}-${t.url}-${i}`}>
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
          <CardTitle>Links that failed to extract ({data.failedTrackers.length})</CardTitle>
          <CardDescription>Trackers currently reporting an extraction error.</CardDescription>
        </CardHeader>
        <CardContent>
          {deleteError ? (
            <p className="mb-3 text-sm text-destructive">{deleteError}</p>
          ) : null}
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>User</TableHead>
                <TableHead>Product</TableHead>
                <TableHead>Error</TableHead>
                <TableHead>Last checked</TableHead>
                <TableHead className="w-10">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.failedTrackers.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={5} className="text-muted-foreground">
                    No failing trackers right now.
                  </TableCell>
                </TableRow>
              ) : (
                data.failedTrackers.map((t) => (
                  <TableRow key={`${t.kind}-${t.id}`}>
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
                        </a>
                        <Badge variant="outline" className="w-fit">
                          {t.kind === 'tracker_error' ? 'tracker' : 'failure log'}
                        </Badge>
                      </div>
                    </TableCell>
                    <TableCell className="max-w-[32rem] whitespace-normal text-destructive">
                      {t.error}
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
        </CardContent>
      </Card>

      <p className="text-xs text-muted-foreground">Generated {formatDate(data.generatedAt)}</p>
    </div>
  )
}
