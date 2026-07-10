import { useCallback, useEffect, useState } from 'react'
import { Activity, Bell, CircleAlert, Clock3, Database, RefreshCw, Server } from 'lucide-react'
import { fetchServiceStatus, type Credentials, type ServiceStatus } from '@/api'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { formatDate } from '@/lib/utils'

export function StatusPage({ credentials, onAuthFailure }: {
  credentials: Credentials
  onAuthFailure: (err: unknown) => void
}) {
  const [data, setData] = useState<ServiceStatus | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [refreshing, setRefreshing] = useState(false)

  const load = useCallback(async () => {
    setRefreshing(true)
    try {
      setData(await fetchServiceStatus(credentials))
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Status check failed')
      onAuthFailure(err)
    } finally {
      setRefreshing(false)
    }
  }, [credentials, onAuthFailure])

  useEffect(() => {
    void load()
    const timer = window.setInterval(() => void load(), 15_000)
    return () => window.clearInterval(timer)
  }, [load])

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">Service health</h1>
          <p className="mt-1 text-sm text-muted-foreground">Live operational overview, refreshed every 15 seconds.</p>
        </div>
        <Button variant="outline" size="sm" disabled={refreshing} onClick={() => void load()}>
          <RefreshCw className={refreshing ? 'animate-spin' : ''} /> Refresh
        </Button>
      </div>

      {error ? (
        <Card className="border-destructive/50 bg-destructive/5">
          <CardContent className="flex items-center gap-3 py-5 text-destructive">
            <CircleAlert className="size-5" /> API or database is unavailable: {error}
          </CardContent>
        </Card>
      ) : null}

      {data ? <>
        <Card className={data.status === 'healthy' ? 'border-emerald-500/30 bg-emerald-500/5' : 'border-amber-500/40 bg-amber-500/5'}>
          <CardHeader>
            <div className="flex items-center justify-between gap-4">
              <div>
                <CardTitle className="flex items-center gap-2"><Activity className="size-5" /> Overall status</CardTitle>
                <CardDescription>Last checked {formatDate(data.checkedAt)}</CardDescription>
              </div>
              <Badge variant={data.status === 'healthy' ? 'secondary' : 'destructive'} className="text-sm">{data.status}</Badge>
            </div>
          </CardHeader>
        </Card>

        <div className="grid gap-4 md:grid-cols-2">
          <StatusCard icon={Database} title="PostgreSQL" status={data.databaseStatus}
            detail={`${data.databaseConnections} open connections`} />
          <StatusCard icon={Server} title="Worker" status={data.workerStatus}
            detail={data.workerLastSeenAt ? `Heartbeat ${ageLabel(data.workerAgeSeconds)} · ${formatDate(data.workerLastSeenAt)}` : 'No heartbeat recorded'} />
        </div>

        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Metric icon={Activity} label="Active trackers" value={data.activeTrackers} />
          <Metric icon={CircleAlert} label="Failing trackers" value={data.failingTrackers} warning={data.failingTrackers > 0} />
          <Metric icon={Clock3} label="Due now" value={data.dueTrackers} warning={data.dueTrackers > 10} />
          <Metric icon={Bell} label="Pending notifications" value={data.pendingNotifications} warning={data.pendingNotifications > 10} />
        </div>
      </> : !error ? <p className="text-muted-foreground">Loading service status…</p> : null}
    </div>
  )
}

function StatusCard({ icon: Icon, title, status, detail }: { icon: typeof Server; title: string; status: string; detail: string }) {
  const good = status === 'healthy'
  return <Card><CardHeader className="pb-2"><div className="flex items-center justify-between"><CardTitle className="flex items-center gap-2 text-base"><Icon className="size-4" />{title}</CardTitle><Badge variant={good ? 'secondary' : 'destructive'}>{status}</Badge></div></CardHeader><CardContent className="text-sm text-muted-foreground">{detail}</CardContent></Card>
}

function Metric({ icon: Icon, label, value, warning = false }: { icon: typeof Activity; label: string; value: number; warning?: boolean }) {
  return <Card className={warning ? 'border-amber-500/40' : undefined}><CardContent className="pt-5"><div className="flex items-center gap-2 text-sm text-muted-foreground"><Icon className="size-4" />{label}</div><div className={warning ? 'mt-2 text-3xl font-semibold text-amber-600' : 'mt-2 text-3xl font-semibold'}>{value}</div></CardContent></Card>
}

function ageLabel(seconds: number | null) {
  if (seconds == null) return 'unknown'
  if (seconds < 60) return `${seconds}s ago`
  return `${Math.floor(seconds / 60)}m ago`
}
