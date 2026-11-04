import { useEffect, useState } from 'react'
import { Lang, t } from './i18n'

export type Me = { id: string; username: string; displayName: string; role: string; language: Lang; theme: string; timezone: string; mustChangePassword: boolean; permissions: string[] }
export const fmt = (s: string | null | undefined, tz: string, l: Lang) => s ? new Date(s).toLocaleString(l === 'id' ? 'id-ID' : 'en-GB', { timeZone: tz, hour12: false }) + ' ' + tz : '-'

export const PER_OPTS = [10, 30, 50, 100]
export function usePager(n: number) {
  const [pg, setPg] = useState(0); const [per, setPer] = useState(10)
  useEffect(() => { setPg(0) }, [n, per])
  const pages = Math.max(1, Math.ceil(n / per)); const cur = Math.min(pg, pages - 1)
  return { n, per, setPer, pages, cur, setPg, from: cur * per, to: cur * per + per }
}
export function Pager({ l, pk }: { l: Lang; pk: ReturnType<typeof usePager> }) {
  const a = pk.n === 0 ? 0 : pk.from + 1; const b = Math.min(pk.to, pk.n)
  return <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 10, flexWrap: 'wrap' }}>
    <span className="mut">{l === 'id' ? 'Baris per halaman' : 'Rows per page'}</span>
    <select value={pk.per} onChange={e => pk.setPer(Number(e.target.value))} style={{ width: 'auto' }}>{PER_OPTS.map(o => <option key={o} value={o}>{o}</option>)}</select>
    <span className="mut">{a}–{b} / {pk.n}</span>
    <button disabled={pk.cur === 0} onClick={() => pk.setPg(0)}>«</button><button disabled={pk.cur === 0} onClick={() => pk.setPg(pk.cur - 1)}>‹</button>
    <span className="mut">{pk.cur + 1} / {pk.pages}</span>
    <button disabled={pk.cur >= pk.pages - 1} onClick={() => pk.setPg(pk.cur + 1)}>›</button><button disabled={pk.cur >= pk.pages - 1} onClick={() => pk.setPg(pk.pages - 1)}>»</button>
  </div>
}
export const SearchBox = ({ l, q, setQ }: { l: Lang; q: string; setQ: (v: string) => void }) => <div style={{ display: 'flex', gap: 10, alignItems: 'center', marginBottom: 10 }}><input placeholder={t(l, 'search')} value={q} onChange={e => setQ(e.target.value)} style={{ maxWidth: 320 }} /></div>
export const has = (q: string, v: any) => !q || JSON.stringify(v).toLowerCase().includes(q.toLowerCase())
export const vkey = (l: Lang, v: string) => { const s = t(l, 'val_' + v); return s.startsWith('val_') ? v : s }
