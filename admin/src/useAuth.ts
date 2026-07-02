import { useState } from 'react'
import { clearCredentials, loadStoredCredentials, storeCredentials, type Credentials } from './api'

// Shared login state: which credentials (if any) are stored, plus setters that also
// persist to localStorage. Kept as a hook (not context) since this is a small,
// single-purpose app — every page needs the same credentials and logout action.
export function useAuth() {
  const [credentials, setCredentialsState] = useState<Credentials | null>(() =>
    loadStoredCredentials(),
  )

  const login = (creds: Credentials) => {
    storeCredentials(creds)
    setCredentialsState(creds)
  }

  const logout = () => {
    clearCredentials()
    setCredentialsState(null)
  }

  return { credentials, login, logout }
}
