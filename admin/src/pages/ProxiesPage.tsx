import { useEffect, useState } from 'react'
import { fetchProxies, type AdminProxy, type Credentials } from '@/api'
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

function statusBadge(status: string) {
  switch (status) {
    case 'alive':
      return <Badge variant="secondary">alive</Badge>
    case 'dead':
      return <Badge variant="destructive">dead</Badge>
    default:
      return <Badge variant="outline">{status}</Badge>
  }
}

export function ProxiesPage({
  credentials,
  onAuthFailure,
}: {
  credentials: Credentials
  onAuthFailure: (err: unknown) => void
}) {
  const [proxies, setProxies] = useState<AdminProxy[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    fetchProxies(credentials)
      .then(setProxies)
      .catch((err) => {
        setError(err.message)
        onAuthFailure(err)
      })
  }, [credentials, onAuthFailure])

  if (error) return <p className="text-destructive">Failed to load: {error}</p>
  if (!proxies) return <p className="text-muted-foreground">Loading…</p>

  const aliveCount = proxies.filter((p) => p.status === 'alive').length

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          Proxy pool ({proxies.length}, {aliveCount} alive)
        </CardTitle>
        <CardDescription>
          Authenticated proxies added manually (e.g. from a paid rotating-proxy
          provider), re-checked for aliveness hourly. Status reflects that check, not any
          claim the provider made about it.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Address</TableHead>
              <TableHead>Country</TableHead>
              <TableHead>Source</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="text-right">Used</TableHead>
              <TableHead>Last checked</TableHead>
              <TableHead>Last used</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {proxies.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7} className="text-muted-foreground">
                  No proxies yet — the pool refreshes hourly, or wait for the worker's
                  next cycle.
                </TableCell>
              </TableRow>
            ) : (
              proxies.map((p) => (
                <TableRow key={p.id}>
                  <TableCell className="font-mono text-xs">
                    {p.address}
                    {p.hasAuth && (
                      <Badge variant="outline" className="ml-2">
                        auth
                      </Badge>
                    )}
                  </TableCell>
                  <TableCell>{p.countryCode || '—'}</TableCell>
                  <TableCell>
                    <Badge variant="outline">{p.source}</Badge>
                  </TableCell>
                  <TableCell>{statusBadge(p.status)}</TableCell>
                  <TableCell className="text-right">{p.useCount}</TableCell>
                  <TableCell className="text-muted-foreground">
                    {p.lastCheckedAt ?? '—'}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {p.lastUsedAt ?? '—'}
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  )
}
