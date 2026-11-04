export class ApiError extends Error { constructor(public status: number, public code: string, msg: string) { super(msg) } }
export async function api<T = any>(method: string, path: string, body?: unknown): Promise<T> {
  const r = await fetch('/api/v1' + path, {
    method, credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'bm-web' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const j = await r.json().catch(() => ({}))
  if (!r.ok) throw new ApiError(r.status, j?.error?.code ?? 'error', j?.error?.message ?? r.statusText)
  return j as T
}
