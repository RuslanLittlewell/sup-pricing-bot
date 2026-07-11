import { useEffect, useState } from 'react'
import {
  addProxies,
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

// countryToFlag turns a 2-letter ISO country code into its flag emoji by mapping each
// letter to its regional-indicator symbol. Falls back to a globe for missing/unknown
// codes so the column always renders something.
function countryToFlag(cc: string): string {
  if (!/^[a-zA-Z]{2}$/.test(cc)) return '🌐'
  const codePoints = cc
    .toUpperCase()
    .split('')
    .map((c) => 0x1f1e6 + c.charCodeAt(0) - 65)
  return String.fromCodePoint(...codePoints)
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

  const [showAdd, setShowAdd] = useState(false)
  const [addText, setAddText] = useState('')
  const [networkType, setNetworkType] = useState<AdminProxy['networkType']>('unknown')
  const [provider, setProvider] = useState('')
  const [asn, setAsn] = useState('')
  const [countryCode, setCountryCode] = useState('')
  const [adding, setAdding] = useState(false)
  const [addError, setAddError] = useState<string | null>(null)
  const [addResult, setAddResult] = useState<{ added: number; invalid: string[] } | null>(null)

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

  function openAdd() {
    setAddText('')
    setAddError(null)
    setAddResult(null)
    setShowAdd(true)
  }

  async function handleAdd() {
    setAdding(true)
    setAddError(null)
    setAddResult(null)
    try {
      const result = await addProxies(credentials, addText, {
        networkType,
        provider,
        asn,
        countryCode,
      })
      setAddResult(result)
      setAddText('')
      setProxies(await fetchProxies(credentials))
    } catch (err) {
      setAddError((err as Error).message)
      onAuthFailure(err)
    } finally {
      setAdding(false)
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
            <Button size="sm" onClick={openAdd}>
              Add proxies
            </Button>
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
        <div className="space-y-8">
        {(['residential', 'mobile', 'datacenter', 'unknown'] as const).map((type) => {
          const rows = proxies.filter((proxy) => proxy.networkType === type)
          return <section key={type}>
          <div className="mb-3 flex items-center gap-2">
            <h3 className="text-base font-semibold capitalize">{type} proxies</h3>
            <Badge variant="outline">{rows.length}</Badge>
          </div>
          <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-10">#</TableHead>
              <TableHead>Address</TableHead>
              <TableHead>Country</TableHead>
              <TableHead>Source</TableHead>
              <TableHead>Provider / ASN</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="text-right">Used</TableHead>
              <TableHead>Last checked</TableHead>
              <TableHead>Last used</TableHead>
              <TableHead></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={10} className="text-muted-foreground">
                  No {type} proxies.
                </TableCell>
              </TableRow>
            ) : (
              rows.map((p, i) => (
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
                  <TableCell>
                    <span
                      className="text-lg"
                      title={p.countryCode || 'unknown'}
                      aria-label={p.countryCode || 'unknown'}
                    >
                      {countryToFlag(p.countryCode)}
                    </span>
                  </TableCell>
                  <TableCell>
                    <Badge variant="outline">{p.source}</Badge>
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    <div>{p.provider || '—'}</div>
                    <div className="font-mono">{p.asn || '—'}</div>
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
        </section>
        })}
        </div>
      </CardContent>

      {showAdd && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
          onClick={() => !adding && setShowAdd(false)}
        >
          <div
            className="w-full max-w-lg rounded-lg border bg-background p-6 shadow-lg"
            onClick={(e) => e.stopPropagation()}
          >
            <h2 className="text-lg font-semibold">Add proxies</h2>
            <p className="mt-1 text-sm text-muted-foreground">
              One per line, in <code className="font-mono">host:port:user:pass</code> format
              (<code className="font-mono">host:port</code> without auth also works). Paste the
              provider's list and submit.
            </p>
            <div className="mt-4 grid grid-cols-2 gap-3">
              <label className="text-sm">
                <span className="mb-1 block text-muted-foreground">Network type</span>
                <select
                  className="h-9 w-full rounded-md border border-input bg-background px-3"
                  value={networkType}
                  onChange={(e) => setNetworkType(e.target.value as AdminProxy['networkType'])}
                >
                  <option value="unknown">Unknown</option>
                  <option value="residential">Residential</option>
                  <option value="mobile">Mobile</option>
                  <option value="datacenter">Datacenter</option>
                </select>
              </label>
              <label className="text-sm">
                <span className="mb-1 block text-muted-foreground">Country code</span>
                <input className="h-9 w-full rounded-md border border-input bg-transparent px-3" maxLength={2} placeholder="PL" value={countryCode} onChange={(e) => setCountryCode(e.target.value)} />
              </label>
              <label className="text-sm">
                <span className="mb-1 block text-muted-foreground">Provider</span>
                <input className="h-9 w-full rounded-md border border-input bg-transparent px-3" placeholder="Provider name" value={provider} onChange={(e) => setProvider(e.target.value)} />
              </label>
              <label className="text-sm">
                <span className="mb-1 block text-muted-foreground">ASN</span>
                <input className="h-9 w-full rounded-md border border-input bg-transparent px-3" placeholder="AS12345" value={asn} onChange={(e) => setAsn(e.target.value)} />
              </label>
            </div>
            <textarea
              className="mt-3 h-48 w-full resize-y rounded-md border border-input bg-transparent px-3 py-2 font-mono text-xs shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              placeholder={'31.59.20.176:6754:user:pass\n45.38.107.97:6014:user:pass'}
              value={addText}
              disabled={adding}
              onChange={(e) => setAddText(e.target.value)}
            />
            {addError && <p className="mt-2 text-sm text-destructive">{addError}</p>}
            {addResult && (
              <div className="mt-2 text-sm">
                <p className="text-green-600">Added {addResult.added} prox{addResult.added === 1 ? 'y' : 'ies'}.</p>
                {addResult.invalid.length > 0 && (
                  <div className="mt-1 text-muted-foreground">
                    Skipped {addResult.invalid.length} unparseable line
                    {addResult.invalid.length === 1 ? '' : 's'}:
                    <pre className="mt-1 max-h-24 overflow-auto rounded bg-muted p-2 font-mono text-xs">
                      {addResult.invalid.join('\n')}
                    </pre>
                  </div>
                )}
                <p className="mt-1 text-muted-foreground">
                  New proxies start as “unknown” — use “Recheck dead &amp; unknown” to validate
                  them now, or wait for the hourly sweep.
                </p>
              </div>
            )}
            <div className="mt-4 flex justify-end gap-2">
              <Button variant="outline" size="sm" disabled={adding} onClick={() => setShowAdd(false)}>
                {addResult ? 'Close' : 'Cancel'}
              </Button>
              <Button size="sm" disabled={adding || !addText.trim()} onClick={handleAdd}>
                {adding ? 'Submitting…' : 'Submit'}
              </Button>
            </div>
          </div>
        </div>
      )}
    </Card>
  )
}
