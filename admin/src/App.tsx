import { Navigate, Route, Routes } from 'react-router-dom'
import { useState } from 'react'
import { UnauthorizedError } from './api'
import { useAuth } from './useAuth'
import { LoginForm } from './LoginForm'
import { Layout } from './Layout'
import { UsersPage } from './pages/UsersPage'
import { TrackersPage } from './pages/TrackersPage'

function App() {
  const { credentials, login, logout } = useAuth()
  const [loginError, setLoginError] = useState<string | null>(null)

  if (!credentials) {
    return (
      <LoginForm
        error={loginError}
        onSubmit={(creds) => {
          setLoginError(null)
          login(creds)
        }}
      />
    )
  }

  // Any page's fetch can hit an expired/rejected credential mid-session — drop it and
  // fall back to the login form instead of leaving the user stuck on a broken page.
  const handleAuthFailure = (err: unknown) => {
    if (err instanceof UnauthorizedError) {
      logout()
      setLoginError(err.message)
    }
  }

  return (
    <Routes>
      <Route element={<Layout onLogout={logout} />}>
        <Route path="/" element={<Navigate to="/users" replace />} />
        <Route
          path="/users"
          element={<UsersPage credentials={credentials} onAuthFailure={handleAuthFailure} />}
        />
        <Route
          path="/trackers"
          element={<TrackersPage credentials={credentials} onAuthFailure={handleAuthFailure} />}
        />
        <Route path="*" element={<Navigate to="/users" replace />} />
      </Route>
    </Routes>
  )
}

export default App
