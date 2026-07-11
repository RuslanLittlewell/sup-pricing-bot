import { useState } from 'react'
import { Braces, Globe2, Play, Search, Wrench } from 'lucide-react'
import { runPlaygroundTool, type Credentials, type PlaygroundResult } from '@/api'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

const groups = [
  {
    title: 'Page delivery',
    description: 'Test how the service reaches and renders the target page.',
    icon: Globe2,
    tools: [['fetch_chain', 'Full fetch chain'], ['renderer', 'Chromium renderer']],
  },
  {
    title: 'Page parsers',
    description: 'Fetch the page and run one parser against the resulting HTML.',
    icon: Braces,
    tools: [['attribute', 'Attribute / JSON-LD'], ['generic', 'Generic parser'], ['stock', 'Stock detector']],
  },
  {
    title: 'Search fallbacks',
    description: 'Run the complete fallback chain or isolate one search provider.',
    icon: Search,
    tools: [['search_fallback', 'Full search chain'], ['searxng', 'SearXNG'], ['openserp', 'OpenSERP'], ['serper', 'Serper'], ['serpapi', 'SerpAPI'], ['gemini', 'Gemini']],
  },
] as const

export function PlaygroundPage({ credentials, onAuthFailure }: { credentials: Credentials; onAuthFailure: (err: unknown) => void }) {
  const [url, setUrl] = useState('')
  const [tool, setTool] = useState('fetch_chain')
  const [running, setRunning] = useState(false)
  const [response, setResponse] = useState<PlaygroundResult | null>(null)
  const [error, setError] = useState<string | null>(null)

  async function run(selected = tool) {
    if (!url.trim()) return
    setTool(selected)
    setRunning(true)
    setError(null)
    setResponse(null)
    try {
      setResponse(await runPlaygroundTool(credentials, selected, url.trim()))
    } catch (err) {
      setError((err as Error).message)
      onAuthFailure(err)
    } finally {
      setRunning(false)
    }
  }

  return <div className="space-y-6">
    <div>
      <Badge variant="outline" className="mb-3"><Wrench className="mr-1 size-3" /> Diagnostics</Badge>
      <h1 className="text-2xl font-semibold tracking-tight">Parser playground</h1>
      <p className="mt-1 text-sm text-muted-foreground">Run one scraper or parser manually without creating a tracker.</p>
    </div>

    <Card className="border-primary/20 bg-gradient-to-br from-card to-primary/5">
      <CardHeader>
        <CardTitle>Target URL</CardTitle>
        <CardDescription>Enter a public product page, choose a tool below, and inspect its raw structured response.</CardDescription>
      </CardHeader>
      <CardContent className="flex gap-3">
        <Input value={url} onChange={(e) => setUrl(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && run()} placeholder="https://shop.example/product/123" className="h-11 font-mono" />
        <Button className="h-11 min-w-28" disabled={running || !url.trim()} onClick={() => run()}><Play className="mr-2 size-4" />{running ? 'Running…' : 'Run'}</Button>
      </CardContent>
    </Card>

    <div className="grid gap-4 xl:grid-cols-3">
      {groups.map((group) => <Card key={group.title}>
        <CardHeader>
          <div className="mb-2 flex size-9 items-center justify-center rounded-lg bg-primary/10 text-primary"><group.icon className="size-4" /></div>
          <CardTitle className="text-base">{group.title}</CardTitle>
          <CardDescription>{group.description}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2">
          {group.tools.map(([value, label]) => <Button key={value} variant={tool === value ? 'secondary' : 'outline'} className="w-full justify-between" disabled={running || !url.trim()} onClick={() => run(value)}>{label}<Play className="size-3.5 opacity-60" /></Button>)}
        </CardContent>
      </Card>)}
    </div>

    <Card>
      <CardHeader className="flex-row items-center justify-between">
        <div><CardTitle>Response</CardTitle><CardDescription>JSON returned by the selected diagnostic tool.</CardDescription></div>
        {response && <div className="flex gap-2"><Badge variant="outline">{response.durationMs} ms</Badge>{response.fetchMethod && <Badge variant="secondary">{response.fetchMethod}</Badge>}</div>}
      </CardHeader>
      <CardContent>
        <pre className="min-h-56 overflow-auto rounded-lg border bg-black/40 p-4 font-mono text-xs leading-6 text-emerald-300">{error ?? (response ? JSON.stringify(response, null, 2) : '// Run a tool to see its response here')}</pre>
      </CardContent>
    </Card>
  </div>
}
