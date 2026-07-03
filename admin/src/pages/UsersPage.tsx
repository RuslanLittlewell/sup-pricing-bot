import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { fetchUsers, type AdminUser, type Credentials } from '@/api'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

export function UsersPage({
  credentials,
  onAuthFailure,
}: {
  credentials: Credentials
  onAuthFailure: (err: unknown) => void
}) {
  const [users, setUsers] = useState<AdminUser[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    fetchUsers(credentials)
      .then(setUsers)
      .catch((err) => {
        setError(err.message)
        onAuthFailure(err)
      })
  }, [credentials, onAuthFailure])

  if (error) return <p className="text-destructive">Failed to load: {error}</p>
  if (!users) return <p className="text-muted-foreground">Loading…</p>

  return (
    <Card>
      <CardHeader>
        <CardTitle>Users ({users.length})</CardTitle>
      </CardHeader>
      <CardContent>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>Name</TableHead>
              <TableHead>Subscription</TableHead>
              <TableHead className="text-right">Trackers</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {users.length === 0 ? (
              <TableRow>
                <TableCell colSpan={4} className="text-muted-foreground">
                  No users yet.
                </TableCell>
              </TableRow>
            ) : (
              users.map((u) => (
                <TableRow key={u.id}>
                  <TableCell className="font-mono text-xs text-muted-foreground">
                    {u.id}
                  </TableCell>
                  <TableCell>
                    <Link
                      to={`/users/${encodeURIComponent(u.id)}/trackers`}
                      className="text-primary underline-offset-4 hover:underline"
                    >
                      {u.name || u.email}
                    </Link>
                    {u.name && (
                      <div className="text-xs text-muted-foreground">{u.email}</div>
                    )}
                  </TableCell>
                  <TableCell>
                    <Badge variant={u.planCode === 'free' ? 'secondary' : 'default'}>
                      {u.planCode}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-right">
                    <Link
                      to={`/users/${encodeURIComponent(u.id)}/trackers`}
                      className="text-primary underline-offset-4 hover:underline"
                    >
                      {u.trackerCount}
                    </Link>
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
