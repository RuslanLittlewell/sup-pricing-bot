import type { ComponentType } from 'react'
import { Activity, Box, ExternalLink, LogOut, Network, Users, Wrench } from 'lucide-react'
import { NavLink, Outlet } from 'react-router-dom'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'

const navigation = [
  { to: '/status', label: 'Status', icon: Activity },
  { to: '/users', label: 'Users', icon: Users },
  { to: '/trackers', label: 'Trackers', icon: Box },
  { to: '/proxies', label: 'Proxies', icon: Network },
  { to: '/playground', label: 'Playground', icon: Wrench },
]

export function Layout({ onLogout }: { onLogout: () => void }) {
  return <div className="dark min-h-screen bg-background text-foreground">
    <aside className="fixed inset-y-0 left-0 z-40 flex w-64 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground">
      <div className="flex h-16 items-center gap-3 border-b border-sidebar-border px-5">
        <div className="flex size-9 items-center justify-center rounded-xl bg-primary text-primary-foreground shadow-lg shadow-primary/10"><Activity className="size-4" /></div>
        <div><div className="text-sm font-semibold">Surprice Admin</div><div className="text-xs text-muted-foreground">Price intelligence</div></div>
      </div>
      <nav className="flex-1 space-y-1 p-3">
        <div className="px-3 pb-2 pt-3 text-[11px] font-medium uppercase tracking-[0.16em] text-muted-foreground">Workspace</div>
        {navigation.map((item) => <NavItem key={item.to} {...item} />)}
      </nav>
      <div className="border-t border-sidebar-border p-3">
        <div className="mb-3 rounded-lg border border-sidebar-border bg-sidebar-accent/40 p-3">
          <div className="flex items-center justify-between"><span className="text-xs font-medium">Production</span><Badge className="bg-emerald-500/15 text-emerald-400">Live</Badge></div>
          <a href="https://surpricebot.com" target="_blank" rel="noreferrer" className="mt-2 flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">surpricebot.com <ExternalLink className="size-3" /></a>
        </div>
        <Button variant="ghost" className="w-full justify-start text-muted-foreground hover:text-foreground" onClick={onLogout}><LogOut className="mr-2 size-4" />Log out</Button>
      </div>
    </aside>
    <main className="min-h-screen pl-64">
      <div className="mx-auto max-w-[1500px] px-8 py-8"><Outlet /></div>
    </main>
  </div>
}

function NavItem({ to, label, icon: Icon }: { to: string; label: string; icon: ComponentType<{ className?: string }> }) {
  return <NavLink to={to} className={({ isActive }) => cn('flex items-center gap-3 rounded-lg px-3 py-2.5 text-sm font-medium transition-colors', isActive ? 'bg-sidebar-accent text-sidebar-accent-foreground shadow-sm' : 'text-muted-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-accent-foreground')}><Icon className="size-4" />{label}</NavLink>
}
