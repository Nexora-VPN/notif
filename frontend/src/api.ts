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

export interface Field {
  key: string
  secret?: boolean
  required?: boolean
  multiline?: boolean
  default?: string
}

export interface Kind {
  name: string
  fields: Field[]
  perMinute: number
}

export interface Channel {
  id: number
  kind: string
  name: string
  enabled: boolean
  position: number
  perMinute: number
  config: Record<string, string>
}

export interface ChannelBody {
  kind?: string
  name: string
  enabled: boolean
  perMinute: number
  config: Record<string, string>
}

export interface Attempt {
  id: number
  channelId: number
  channelName: string
  at: number
  outcome: string
  detail: string
}

export interface Delivery {
  id: number
  key: string
  userId: number
  userName: string
  kind: string
  title: string
  body: string
  urgent: boolean
  status: string
  nextAt: number
  channelId: number
  channelName: string
  error: string
  createdAt: number
  sentAt: number
  attempts?: Attempt[]
}

export interface DeliverySettings {
  timeZone: string
  quietEnabled: boolean
  quietFrom: string
  quietTo: string
  language: string
  retentionDays: number
}

export interface Summary {
  today: Record<string, number>
  byChannel: { channelId: number; name: string; sent: number }[] | null
  waiting: number
  held: number
  users: number
  channels: number
  quiet: boolean
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
  summary: () => call<Summary>('GET', '/summary'),
  kinds: () => call<Kind[]>('GET', '/channel-kinds'),
  channels: () => call<Channel[]>('GET', '/channels'),
  createChannel: (b: ChannelBody) => call<Channel>('POST', '/channels', b),
  updateChannel: (id: number, b: ChannelBody) => call<Channel>('PUT', '/channels/' + id, b),
  deleteChannel: (id: number) => call<void>('DELETE', '/channels/' + id),
  orderChannels: (ids: number[]) => call<void>('PUT', '/channels/order', { ids }),
  test: (user: string, channel = 0, quiet = false) =>
    call<Delivery>('POST', '/test', { user, channel, quiet }),
  deliveries: (q: { status?: string; user?: string; limit: number; offset: number }) => {
    const p = new URLSearchParams({ limit: String(q.limit), offset: String(q.offset) })
    if (q.status) p.set('status', q.status)
    if (q.user) p.set('user', q.user)
    return call<{ items: Delivery[]; total: number }>('GET', '/deliveries?' + p.toString())
  },
  delivery: (id: number) => call<Delivery>('GET', '/deliveries/' + id),
  cancelDelivery: (id: number) => call<void>('POST', '/deliveries/' + id + '/cancel'),
  deliverySettings: () =>
    call<{ settings: DeliverySettings; serverZone: string }>('GET', '/settings/delivery'),
  saveDeliverySettings: (d: DeliverySettings) => call<void>('PUT', '/settings/delivery', d),
}
