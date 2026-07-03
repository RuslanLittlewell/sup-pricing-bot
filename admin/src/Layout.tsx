import type { ReactNode } from 'react'
import { NavLink, Outlet } from 'react-router-dom'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'

export function Layout({ onLogout }: { onLogout: () => void }) {
  return (
    <div className="min-h-screen bg-muted/20">
      <header className="border-b bg-background">
        <div className="mx-auto flex max-w-5xl items-center justify-between px-6 py-4">
          <div className="flex items-center gap-6">
            <span className="font-semibold">Price Tracker — Admin</span>
            <nav className="flex gap-1">
              <NavItem to="/users">Users</NavItem>
              <NavItem to="/trackers">Trackers</NavItem>
              <NavItem to="/proxies">Proxies</NavItem>
            </nav>
          </div>
          <Button variant="ghost" size="sm" onClick={onLogout}>
            Log out
          </Button>
        </div>
      </header>
      <main className="mx-auto max-w-5xl px-6 py-8">
        <Outlet />
      </main>
    </div>
  )
}

function NavItem({ to, children }: { to: string; children: ReactNode }) {
  return (
    <NavLink
      to={to}
      className={({ isActive }) =>
        cn(
          'rounded-md px-3 py-1.5 text-sm font-medium transition-colors',
          isActive
            ? 'bg-primary text-primary-foreground'
            : 'text-muted-foreground hover:bg-muted hover:text-foreground',
        )
      }
    >
      {children}
    </NavLink>
  )
}
