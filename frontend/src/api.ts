// The admin web's calls to Notif's own /api. The session is an HttpOnly
// cookie, so nothing here holds a token; a 401 sends the admin to sign in.

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message)
  }
}

let onSignedOut: () => void = () => {}

export function whenSignedOut(f: () => void) {
  onSignedOut = f
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch('api' + path, {
    method,
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
  })
  if (res.status === 401 && !path.startsWith('/login')) onSignedOut()
  if (!res.ok) {
    let msg = res.statusText
    try {
      msg = (await res.json()).error ?? msg
    } catch {
      // not JSON
    }
    throw new ApiError(res.status, msg)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export interface Me {
  id: number
  username: string
  totpEnabled: boolean
  lastLoginAt: number
}

export interface Status {
  version: string
  database: string
  claimCode?: string
  panel?: { id: string; url: string; version: string; registeredAt: number }
  scopes: string[]
  accounts?: number
  panelError?: string
}

export const api = {
  login: (username: string, password: string) =>
    call<Me | { mfa: true; token: string }>('POST', '/login', { username, password }),
  loginMFA: (token: string, code: string) => call<Me>('POST', '/login/mfa', { token, code }),
  logout: () => call<void>('POST', '/logout'),
  me: () => call<Me>('GET', '/me'),
  password: (current: string, next: string) =>
    call<void>('PUT', '/me/password', { current, new: next }),
  totpEnrol: () => call<{ secret: string; uri: string }>('POST', '/me/2fa/totp'),
  totpConfirm: (code: string) => call<void>('POST', '/me/2fa/totp/confirm', { code }),
  totpDisable: (password: string) => call<void>('DELETE', '/me/2fa', { password }),
  status: () => call<Status>('GET', '/status'),
}
