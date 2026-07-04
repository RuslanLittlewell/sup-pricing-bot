import { useEffect, useState } from 'react'
import {
  checkProxy,
  deleteDeadProxies,
  fetchProxies,
  recheckDeadProxies,
  type AdminProxy,
  type Credentials,
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
  const [deleting, setDeleting] = useState(false)
  const [checkingId, setCheckingId] = useState<string | null>(null)
  const [rechecking, setRechecking] = useState(false)

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
  const deadCount = proxies.filter((p) => p.status === 'dead').length
  const deadOrUnknownCount = proxies.filter((p) => p.status !== 'alive').length

  async function handleDeleteDead() {
    setDeleting(true)
    try {
      await deleteDeadProxies(credentials)
      setProxies(await fetchProxies(credentials))
    } catch (err) {
      setError((err as Error).message)
      onAuthFailure(err)
    } finally {
      setDeleting(false)
    }
  }

  async function handleRecheckDead() {
    setRechecking(true)
    try {
      await recheckDeadProxies(credentials)
      setProxies(await fetchProxies(credentials))
    } catch (err) {
      setError((err as Error).message)
      onAuthFailure(err)
    } finally {
      setRechecking(false)
    }
  }

  async function handleCheckOne(id: string) {
    setCheckingId(id)
    try {
      const { alive } = await checkProxy(credentials, id)
      setProxies(
        (prev) =>
          prev?.map((p) =>
            p.id === id
              ? { ...p, status: alive ? 'alive' : 'dead', lastCheckedAt: new Date().toISOString() }
              : p,
          ) ?? null,
      )
    } catch (err) {
      setError((err as Error).message)
      onAuthFailure(err)
    } finally {
      setCheckingId(null)
    }
  }

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between gap-4">
          <div>
            <CardTitle>
              Proxy pool ({proxies.length}, {aliveCount} alive, {deadCount} dead)
            </CardTitle>
            <CardDescription>
              Authenticated proxies added manually (e.g. from a paid rotating-proxy
              provider), re-checked for aliveness hourly. Status reflects that check, not
              any claim the provider made about it.
            </CardDescription>
          </div>
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={deadOrUnknownCount === 0 || rechecking}
              onClick={handleRecheckDead}
            >
              {rechecking ? 'Rechecking…' : `Recheck dead & unknown (${deadOrUnknownCount})`}
            </Button>
            <Button
              variant="destructive"
              size="sm"
              disabled={deadCount === 0 || deleting}
              onClick={handleDeleteDead}
            >
              {deleting ? 'Deleting…' : `Delete dead proxies (${deadCount})`}
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-10">#</TableHead>
              <TableHead>Address</TableHead>
              <TableHead>Country</TableHead>
              <TableHead>Source</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="text-right">Used</TableHead>
              <TableHead>Last checked</TableHead>
              <TableHead>Last used</TableHead>
              <TableHead></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {proxies.length === 0 ? (
              <TableRow>
                <TableCell colSpan={9} className="text-muted-foreground">
                  No proxies yet — the pool refreshes hourly, or wait for the worker's
                  next cycle.
                </TableCell>
              </TableRow>
            ) : (
              proxies.map((p, i) => (
                <TableRow key={p.id}>
                  <TableCell className="text-muted-foreground">{i + 1}</TableCell>
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
                    {formatDate(p.lastCheckedAt)}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {formatDate(p.lastUsedAt)}
                  </TableCell>
                  <TableCell>
                    <Button
                      variant="outline"
                      size="xs"
                      disabled={checkingId === p.id}
                      onClick={() => handleCheckOne(p.id)}
                    >
                      {checkingId === p.id ? 'Pinging…' : 'Ping'}
                    </Button>
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
