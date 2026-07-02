import { useEffect, useState } from 'react'
import { fetchTrackers, type TrackersResponse, type Credentials } from '@/api'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
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

  useEffect(() => {
    fetchTrackers(credentials)
      .then(setData)
      .catch((err) => {
        setError(err.message)
        onAuthFailure(err)
      })
  }, [credentials, onAuthFailure])

  if (error) return <p className="text-destructive">Failed to load: {error}</p>
  if (!data) return <p className="text-muted-foreground">Loading…</p>

  return (
    <div className="flex flex-col gap-6">
      <Card>
        <CardHeader>
          <CardTitle>Resolved via a search fallback ({data.fallbackTrackers.length})</CardTitle>
          <CardDescription>
            Trackers whose price came from a paid token/API-key service (SerpApi, Serper,
            OpenSERP) instead of reading the page directly.
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
                    <TableCell className="text-muted-foreground">{t.timestamp}</TableCell>
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
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>User</TableHead>
                <TableHead>Product</TableHead>
                <TableHead>Error</TableHead>
                <TableHead>Last checked</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data.failedTrackers.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={4} className="text-muted-foreground">
                    No failing trackers right now.
                  </TableCell>
                </TableRow>
              ) : (
                data.failedTrackers.map((t, i) => (
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
                    <TableCell className="text-destructive">{t.error}</TableCell>
                    <TableCell className="text-muted-foreground">{t.timestamp}</TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <p className="text-xs text-muted-foreground">Generated {data.generatedAt}</p>
    </div>
  )
}
