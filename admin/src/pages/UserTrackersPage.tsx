import { useEffect, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { ArrowLeft } from 'lucide-react'
import { fetchUserTrackers, type UserTracker, type Credentials } from '@/api'
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

// Extraction methods that read the page directly, for free — everything else came from a
// paid search fallback tier. Mirrors adminSearchExtractionMethods in backend/internal/handler/admin.go.
const SEARCH_FALLBACK_METHODS = new Set([
  'openserp_search_result',
  'openserp_extract',
  'serper_organic_result',
  'serper_shopping_result',
  'serpapi_rich_snippet',
])

function methodBadge(method: string) {
  if (method === 'unknown' || method === '') {
    return <Badge variant="secondary">unknown</Badge>
  }
  return (
    <Badge variant={SEARCH_FALLBACK_METHODS.has(method) ? 'destructive' : 'outline'}>
      {method}
    </Badge>
  )
}

function formatPrice(price: number | null, currency: string) {
  if (price === null) return '—'
  return `${price.toFixed(2)} ${currency}`
}

// How the page body was fetched, independent of how the price was parsed out of it — the
// same extraction method (e.g. json_ld) can come from a page fetched three different
// ways. Mirrors extractor.FetchMethod* in backend/internal/extractor/fetcher.go.
function fetchMethodBadge(method: string) {
  const label = method === 'cf_relay' ? 'relay' : method
  return (
    <Badge variant={method === 'cf_relay' ? 'default' : 'outline'} className="text-xs">
      {label}
    </Badge>
  )
}

export function UserTrackersPage({
  credentials,
  onAuthFailure,
}: {
  credentials: Credentials
  onAuthFailure: (err: unknown) => void
}) {
  const { id } = useParams<{ id: string }>()
  const [trackers, setTrackers] = useState<UserTracker[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!id) return
    setTrackers(null)
    setError(null)
    fetchUserTrackers(credentials, id)
      .then(setTrackers)
      .catch((err) => {
        setError(err.message)
        onAuthFailure(err)
      })
  }, [credentials, id, onAuthFailure])

  return (
    <div className="flex flex-col gap-4">
      <Link
        to="/users"
        className="inline-flex w-fit items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="size-4" />
        Back to users
      </Link>

      <Card>
        <CardHeader>
          <CardTitle>Trackers{trackers ? ` (${trackers.length})` : ''}</CardTitle>
          <CardDescription>
            How each tracker resolves its price — reading the page directly (json_ld,
            dom_attribute, meta_tag, microdata, css_selector, css_text) versus a paid search
            fallback. "Latest" only appears when a check since creation used a different
            method than the one it was set up with. "Fetched via" shows which tier actually
            retrieved the page (direct / cf_relay / render) on the most recent check.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {error ? (
            <p className="text-destructive">Failed to load: {error}</p>
          ) : !trackers ? (
            <p className="text-muted-foreground">Loading…</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Product</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Price</TableHead>
                  <TableHead>Method</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead>Last checked</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {trackers.length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={6} className="text-muted-foreground">
                      No trackers for this user.
                    </TableCell>
                  </TableRow>
                ) : (
                  trackers.map((t) => (
                    <TableRow key={t.id}>
                      <TableCell>
                        <div className="flex max-w-[24rem] flex-col gap-1 whitespace-normal">
                          <a
                            href={t.url}
                            target="_blank"
                            rel="noreferrer"
                            className="text-primary underline-offset-4 hover:underline"
                          >
                            {t.title}
                          </a>
                          <span className="text-xs text-muted-foreground">{t.domain}</span>
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-col gap-1">
                          <Badge variant={t.status === 'active' ? 'secondary' : 'outline'}>
                            {t.status}
                          </Badge>
                          {t.lastError ? (
                            <span className="max-w-[16rem] whitespace-normal text-xs text-destructive">
                              {t.lastError}
                            </span>
                          ) : null}
                        </div>
                      </TableCell>
                      <TableCell className="whitespace-nowrap">
                        <div className="flex flex-col gap-0.5">
                          <span>{formatPrice(t.currentPrice, t.currency)}</span>
                          <span className="text-xs text-muted-foreground">
                            initial {formatPrice(t.initialPrice, t.currency)}
                          </span>
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-col gap-1">
                          {methodBadge(t.extractionMethod)}
                          {t.latestExtractionMethod ? (
                            <span className="text-xs text-muted-foreground">
                              latest: {methodBadge(t.latestExtractionMethod)}
                            </span>
                          ) : null}
                          {t.latestFetchMethod ? (
                            <span className="flex items-center gap-1 text-xs text-muted-foreground">
                              fetched via: {fetchMethodBadge(t.latestFetchMethod)}
                            </span>
                          ) : null}
                        </div>
                      </TableCell>
                      <TableCell className="text-muted-foreground">{t.createdAt}</TableCell>
                      <TableCell className="text-muted-foreground">
                        {t.lastCheckedAt ?? '—'}
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
