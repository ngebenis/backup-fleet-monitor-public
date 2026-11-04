import { useEffect, useState } from 'react'
import { api, ApiError } from './api'
import { Lang, t } from './i18n'

type Me = { id: string; username: string; displayName: string; role: string; language: Lang; theme: string; timezone: string; mustChangePassword: boolean; permissions: string[] }
const fmt = (s: string | null | undefined, tz: string, l: Lang) => s ? new Date(s).toLocaleString(l === 'id' ? 'id-ID' : 'en-GB', { timeZone: tz, hour12: false }) + ' ' + tz : '-'

function applyTheme(th: string) {
  const dark = th === 'dark' || (th === 'system' && matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.dataset.theme = dark ? 'dark' : 'light'
}

export function App() {
  const [me, setMe] = useState<Me | null | undefined>(undefined)
  const [page, setPage] = useState('overview')
  useEffect(() => { api<Me>('GET', '/me').then(setMe).catch(() => setMe(null)) }, [])
  useEffect(() => { if (me) applyTheme(me.theme) }, [me?.theme])
  if (me === undefined) return null
  if (me === null) return <Login onOk={setMe} />
  const l = me.language
  const can = (p: string) => me.permissions.includes(p)
  if (me.mustChangePassword) return <main><h2>{t(l, 'app')}</h2><p className="err">{t(l, 'mustChange')}</p><ChangePw me={me} onDone={() => api<Me>('GET', '/me').then(setMe)} /></main>
  const groups: [string, [string, string, boolean][]][] = [
    [t(l, 'grpOverview'), [['overview', 'overview', true], ['audit', 'audit', can('audit.read')]]],
    [t(l, 'grpInfra'), [['sources', 'navSources', can('sources.read')], ['workloads', 'workloads', can('jobs.read')], ['storage', 'navStorage', can('jobs.read')], ['reports', 'navReports', can('jobs.read')]]],
    [t(l, 'grpAccess'), [['users', 'users', can('users.read')]]],
    [t(l, 'grpSystem'), [['profile', 'profile', true]]],
  ]
  const ico: Record<string, string> = {
    overview: 'M3 3h7v9H3zM14 3h7v5h-7zM14 12h7v9h-7zM3 16h7v5H3z',
    audit: 'M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9zM14 3v6h6M8 13h8M8 17h8',
    sources: 'M4 6c0-1.7 3.6-3 8-3s8 1.3 8 3-3.6 3-8 3-8-1.3-8-3zM4 6v6c0 1.7 3.6 3 8 3s8-1.3 8-3V6M4 12v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6',
    reports: 'M9 17v-6M13 17V7M17 17v-3M4 4h16v16H4z',
    storage: 'M3 7h18v4H3zM3 13h18v4H3zM7 9h.01M7 15h.01',
    workloads: 'M12 2 2 7l10 5 10-5zM2 17l10 5 10-5M2 12l10 5 10-5',
    users: 'M17 21v-2a4 4 0 0 0-4-4H7a4 4 0 0 0-4 4v2M10 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM21 21v-2a4 4 0 0 0-3-3.9M17 3.1a4 4 0 0 1 0 7.8',
    profile: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z',
  }
  const Ic = ({ k }: { k: string }) => <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d={ico[k]} /></svg>
  const title = t(l, page === 'sources' ? 'navSources' : page === 'storage' ? 'navStorage' : page === 'reports' ? 'navReports' : page)
  return <div className="shell">
    <aside className="sb">
      <div className="sb-brand"><img className="sb-logo" src="/logo.svg" alt=""/><div><div className="sb-name">{t(l, 'app')}</div><div className="sb-ver">v1.0</div></div></div>
      <nav className="sb-nav">{groups.map(([g, items]) => items.some(i => i[2]) && <div key={g}><div className="sb-sec">{g}</div>
        {items.filter(i => i[2]).map(([p, lbl]) => <button key={p} className={'sb-item' + (page === p ? ' on' : '')} onClick={() => setPage(p)}><Ic k={p} />{t(l, lbl)}</button>)}</div>)}</nav>
      <div className="sb-user"><div className="sb-av">{(me.displayName || me.username).slice(0, 1).toUpperCase()}</div>
        <div className="sb-uinfo"><div className="sb-uname">{me.displayName || me.username}</div><div className="sb-urole">{me.role}</div></div>
        <button className="sb-out" title={t(l, 'logout')} aria-label={t(l, 'logout')} onClick={async () => { await api('POST', '/auth/logout'); setMe(null) }}><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9"/></svg></button></div>
    </aside>
    <div className="mn">
      <div className="tb"><span className="tb-title">{title}</span><span className="tb-sp" /><span className="tb-chip">{me.timezone}</span>
        <button className="tb-btn" title="Language" onClick={async () => setMe(await api<Me>('PATCH', '/me/preferences', { language: l === 'id' ? 'en' : 'id' }))}>{l === 'id' ? 'ID' : 'EN'}</button>
        <button className="tb-btn" title={t(l, 'theme')} onClick={async () => { const dark = document.documentElement.dataset.theme === 'dark'; setMe(await api<Me>('PATCH', '/me/preferences', { theme: dark ? 'light' : 'dark' })) }}>{document.documentElement.dataset.theme === 'dark' ? '☾ Dark' : '☀ Light'}</button>
      </div>
      <main>
        {page === 'overview' && <Overview me={me} />}
        {page === 'sources' && <Sources me={me} can={can} />}
        {page === 'workloads' && <Workloads me={me} />}
        {page === 'storage' && <Storage me={me} />}
        {page === 'reports' && <Reports me={me} />}
        {page === 'users' && (can('users.read') ? <Users me={me} /> : <p>{t(l, 'forbidden')}</p>)}
        {page === 'audit' && (can('audit.read') ? <Audit me={me} /> : <p>{t(l, 'forbidden')}</p>)}
        {page === 'profile' && <Profile me={me} setMe={setMe} />}
      </main>
    </div>
  </div>
}

function Login({ onOk }: { onOk: (m: Me) => void }) {
  const [u, setU] = useState(''); const [p, setP] = useState(''); const [e, setE] = useState(''); const [busy, setBusy] = useState(false)
  const l: Lang = navigator.language.startsWith('id') ? 'id' : 'en'
  useEffect(() => { document.documentElement.dataset.theme = 'dark' }, [])
  return <div className="lg-page"><div className="lg-card">
    <img className="lg-logo" src="/logo.svg" alt="Backup Monitor"/>
    <h1 className="lg-title">{t(l, 'app')}</h1>
    <div className="lg-sub">Veeam · Proxmox VE · Synology Active Backup</div>
    <form className="lg-form" onSubmit={async ev => { ev.preventDefault(); setE(''); setBusy(true); try { onOk(await api<Me>('POST', '/auth/login', { username: u, password: p })) } catch (x) { setE(x instanceof ApiError ? x.message : t(l, 'error')) } finally { setBusy(false) } }}>
      <label>{t(l, 'username')}<input value={u} onChange={x => setU(x.target.value)} autoComplete="username" autoFocus required /></label>
      <label>{t(l, 'password')}<input type="password" value={p} onChange={x => setP(x.target.value)} autoComplete="current-password" required /></label>
      {e && <div className="err" role="alert">{t(l, 'loginFailed')}: {e}</div>}
      <button className="lg-btn" disabled={busy}><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round"><path d="M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4"/><polyline points="10 17 15 12 10 7"/><line x1="15" y1="12" x2="3" y2="12"/></svg>{t(l, 'login')}</button>
    </form>
    <div className="lg-badges"><span className="bd-g">Veeam BEM</span><span className="bd-b">Proxmox VE</span><span className="bd-g">Synology ABB</span><span className="bd-b">Docker Ready</span></div>
  </div></div>
}

function Overview({ me }: { me: Me }) {
  const l = me.language
  const [d, setD] = useState<any>(null)
  useEffect(() => { api('GET', '/dashboard/summary').then(setD).catch(() => setD({ error: true })) }, [])
  if (!d) return null
  return <>
    <div className="stats">
      <div className="stat"><div className="st-ic b"><svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M4 6c0-1.7 3.6-3 8-3s8 1.3 8 3-3.6 3-8 3-8-1.3-8-3zM4 6v6c0 1.7 3.6 3 8 3s8-1.3 8-3V6"/></svg></div><div><div className="st-n">{d.sourcesTotal ?? '-'}</div><div className="st-l">{t(l, 'sources')}</div><div className="st-s">{d.sourcesEnabled ?? 0} {t(l, 'sourcesEnabled')}</div></div></div>
      <div className="stat"><div className="st-ic g"><svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M22 12h-4l-3 9L9 3l-3 9H2"/></svg></div><div><div className="st-t">{fmt(d.pollerLastBeat, me.timezone, l)}</div><div className="st-l">{t(l, 'pollerBeat')}</div></div></div>
      <div className="stat"><div className="st-ic p"><svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></svg></div><div><div className="st-t">{fmt(d.generatedAt, me.timezone, l)}</div><div className="st-l">{t(l, 'generated')}</div></div></div>
    </div>
    <div className="card" role="status">{d.dataState === 'no_sources' ? t(l, 'noSources') : ''}</div>
    {d.daily && <DailyReport l={l} d={d.daily} tz={me.timezone} />}
  </>
}

function DailyReport({ l, d, tz }: { l: Lang; d: any; tz: string }) {
  const id = l === 'id'
  const G = '#3fb950', R = '#f85149', Y = '#d29922'
  const rows: [string, string][] = [['proxmox', 'PVE vzdump'], ['abb', 'NAS Active Backup'], ['veeam', 'Veeam BEM']]
  const P = d.platforms || {}
  const max = Math.max(1, ...rows.map(([k]) => Math.max(P[k]?.success || 0, P[k]?.failed || 0, P[k]?.warning || 0)))
  const bar = (v: number, c: string, key: string) => <div key={key} style={{ display: 'flex', alignItems: 'center', gap: 8, margin: '2px 0' }}>
    <div style={{ width: Math.max(2, (v / max) * 100) + '%', maxWidth: '85%', height: 14, background: c, opacity: v ? 1 : 0.15, borderRadius: 2 }} />
    <b style={{ color: c }}>{v}</b></div>
  const tr: any[] = d.abbTrend || []
  const tmax = Math.max(1, ...tr.map(x => Math.max(x.success, x.warning, x.failed)))
  const W = 640, H = 170, pl = 28, pb = 22, bw = (W - pl) / Math.max(1, tr.length)
  const y = (v: number) => H - pb - (v / tmax) * (H - pb - 10)
  return <div className="card">
    <h3>{t(l, 'dbTitle')} · {new Date().toLocaleDateString('en-CA', { timeZone: tz })}</h3>
    <div className="mut">{t(l, 'dbSub')}</div>
    <div className="mut" style={{ fontSize: 12 }}><span style={{ color: G }}>■</span> {t(l, 'dbOk')} <span style={{ color: R }}>■</span> {t(l, 'dbFail')} <span style={{ color: Y }}>■</span> {t(l, 'dbWarn')}</div>
    {rows.map(([k, label]) => <div key={k} style={{ display: 'grid', gridTemplateColumns: '150px 1fr', alignItems: 'start', marginTop: 8 }}>
      <div>{label}</div>
      <div>{bar(P[k]?.success || 0, G, 's')}{(P[k]?.warning || 0) > 0 && bar(P[k].warning, Y, 'w')}{(P[k]?.failed || 0) > 0 && bar(P[k].failed, R, 'f')}</div>
    </div>)}
    <h4 style={{ marginTop: 16 }}>{t(l, 'dbTrend')}</h4>
    <svg viewBox={`0 0 ${W} ${H}`} style={{ width: '100%', maxWidth: W }} role="img" aria-label="ABB trend">
      {[0, 0.5, 1].map(f => <g key={f}><line x1={pl} x2={W} y1={y(tmax * f)} y2={y(tmax * f)} stroke="currentColor" opacity={0.15} /><text x={0} y={y(tmax * f) + 4} fontSize="10" fill="currentColor" opacity={0.6}>{Math.round(tmax * f)}</text></g>)}
      {tr.map((x, i) => { const x0 = pl + i * bw + 2, w = (bw - 4) / 3; return <g key={x.day}>
        <rect x={x0} y={y(x.success)} width={w} height={H - pb - y(x.success)} fill={G}><title>{x.day} ok {x.success}</title></rect>
        <rect x={x0 + w} y={y(x.warning)} width={w} height={H - pb - y(x.warning)} fill={Y}><title>{x.day} warning {x.warning}</title></rect>
        <rect x={x0 + 2 * w} y={y(x.failed)} width={w} height={H - pb - y(x.failed)} fill={R}><title>{x.day} failed {x.failed}</title></rect>
        <text x={x0 + bw / 2 - 2} y={H - 8} fontSize="10" textAnchor="middle" fill="currentColor" opacity={0.7}>{x.day.slice(8)}</text></g> })}
    </svg>
    <div className="mut" style={{ marginTop: 8 }}>
      {t(l, 'dbCov')} {d.pve?.covered}/{d.pve?.total} · {t(l, 'dbMiss')} {d.pve?.missing} VM · {t(l, 'dbFail')}: PVE {P.proxmox?.failed || 0}, NAS {P.abb?.failed || 0}, Veeam {P.veeam?.failed || 0}</div>
    {d.veeamNotRun > 0 && <div style={{ color: Y }}>{t(l, 'dbVeeamNR')}: {d.veeamNotRun}</div>}
  </div>
}

function Users({ me }: { me: Me }) {
  const l = me.language; const [rows, setRows] = useState<any[]>([]); const [q, setQ] = useState('')
  useEffect(() => { api('GET', '/users').then(setRows).catch(() => {}) }, [])
  const roles: [string, string, string][] = [['super_admin', '#ef4444', l === 'id' ? 'Akses penuh' : 'Full access'], ['administrator', '#f59e0b', l === 'id' ? 'Kelola sumber, alert, laporan, setelan' : 'Manage sources, alerts, reports, settings'], ['backup_operator', '#4f8ff7', l === 'id' ? 'Lihat data, ack alert, ekspor laporan' : 'View data, acknowledge alerts, export reports'], ['auditor', '#22c55e', l === 'id' ? 'Baca saja termasuk audit log' : 'Read only incl. audit log']]
  const known = new Set(roles.map(r => r[0])); const extra = Array.from(new Set(rows.map(r => r.role))).filter(x => !known.has(x))
  const all: [string, string, string][] = [...roles, ...extra.map(x => [x, '#8b5cf6', ''] as [string, string, string])]
  const cnt = (r: string) => rows.filter(x => x.role === r).length
  const fl = rows.filter(r => has(q, [r.username, r.role])); const pk = usePager(fl.length)
  return <>
    <div className="rolegrid">{all.map(([r, c, d]) => <div key={r} className="rolecard" style={{ borderTop: '2px solid ' + c }}>
      <div className="rc-h"><b style={{ color: c }}>{r}</b><span className="rc-n">{cnt(r)}</span></div><div className="rc-d">{d}</div></div>)}</div>
    <div className="card" style={{ overflowX: 'auto' }}><SearchBox l={l} q={q} setQ={setQ} /><table><thead><tr><th>{t(l, 'username')}</th><th>{t(l, 'role')}</th><th>{t(l, 'active')}</th></tr></thead>
      <tbody>{fl.slice(pk.from, pk.to).map(r => <tr key={r.id}><td>{r.username}</td><td><span className="pill" style={{ color: (all.find(x => x[0] === r.role) || [])[1] as string }}>{r.role}</span></td>
        <td><span className={'tg ' + (r.active ? 'on' : 'off')} /> {r.active ? t(l, 'active') : '—'}</td></tr>)}</tbody></table><Pager l={l} pk={pk} /></div>
  </>
}

function Audit({ me }: { me: Me }) {
  const l = me.language; const [rows, setRows] = useState<any[]>([]); const [q, setQ] = useState('')
  useEffect(() => { api('GET', '/audit-logs?limit=500').then(setRows).catch(() => {}) }, [])
  const n = (f: (r: any) => boolean) => rows.filter(f).length
  const cards: [string, number, string][] = [
    [t(l, 'loginOk'), n(r => r.action === 'auth.login' && r.outcome === 'success'), '#22c55e'],
    [t(l, 'loginFail'), n(r => r.action === 'auth.login' && r.outcome !== 'success'), '#ef4444'],
    [l === 'id' ? 'Ubah sumber' : 'Source changes', n(r => /^source\.(create|update|delete|enable|disable)/.test(r.action)), '#4f8ff7'],
    [l === 'id' ? 'Test / poll' : 'Test / poll', n(r => /^source\.(test|poll)/.test(r.action)), '#8b5cf6'],
    [t(l, 'pwChange'), n(r => r.action === 'auth.password_change'), '#f59e0b'],
    [t(l, 'totalEvents'), rows.length, '#8b93a1'],
  ]
  const fl = rows.filter(r => !q || JSON.stringify([r.actor, r.action, r.ip, r.outcome]).toLowerCase().includes(q.toLowerCase()))
  const pk = usePager(fl.length)
  return <>
    <div className="sumgrid">{cards.map(([k, v, c]) => <div key={k} className="sum"><div className="sum-n" style={{ color: c }}>{v}</div><div className="sum-l">{k}</div></div>)}</div>
    <div className="card" style={{ overflowX: 'auto' }}>
      <div style={{ display: 'flex', gap: 10, alignItems: 'center', marginBottom: 10 }}><input placeholder={l === 'id' ? 'Cari pelaku, aksi, IP…' : 'Search actor, action, IP…'} value={q} onChange={e => setQ(e.target.value)} style={{ maxWidth: 320 }} />
        <span className="mut">{fl.length} event</span></div>
      <table><thead><tr><th>{t(l, 'time')}</th><th>{t(l, 'actor')}</th><th>{t(l, 'action')}</th><th>{t(l, 'outcome')}</th><th>{t(l, 'ip')}</th></tr></thead>
        <tbody>{fl.slice(pk.from, pk.to).map(r => <tr key={r.id}><td>{fmt(r.ts, me.timezone, l)}</td><td>{r.actor}</td><td><span className="pill">{r.action}</span></td>
          <td><span className="pill" style={{ color: r.outcome === 'success' ? '#22c55e' : '#ef4444' }}>{vkey(l, r.outcome)}</span></td><td>{r.ip}</td></tr>)}</tbody></table>
      <Pager l={l} pk={pk} />
    </div>
  </>
}

function ChangePw({ me, onDone }: { me: Me; onDone: () => void }) {
  const l = me.language; const [c, setC] = useState(''); const [n, setN] = useState(''); const [m, setM] = useState('')
  return <form onSubmit={async ev => { ev.preventDefault(); setM(''); try { await api('POST', '/me/password', { current: c, new: n }); setC(''); setN(''); setM(t(l, 'saved')); onDone() } catch (x) { setM(x instanceof ApiError ? x.message : t(l, 'error')) } }}>
    <label>{t(l, 'currentPw')}<input type="password" value={c} onChange={x => setC(x.target.value)} autoComplete="current-password" required /></label>
    <label>{t(l, 'newPw')}<input type="password" value={n} onChange={x => setN(x.target.value)} autoComplete="new-password" minLength={12} required /></label>
    {m && <div role="status">{m}</div>}<button className="pri">{t(l, 'changePw')}</button></form>
}

function Profile({ me, setMe }: { me: Me; setMe: (m: Me) => void }) {
  const l = me.language
  const tzs: string[] = (Intl as any).supportedValuesOf ? (Intl as any).supportedValuesOf('timeZone') : ['Asia/Jakarta', 'UTC']
  const upd = async (p: Partial<Pick<Me, 'language' | 'theme' | 'timezone'>>) => setMe(await api<Me>('PATCH', '/me/preferences', p))
  return <>
    <div className="card"><div className="grid">
      <label>{t(l, 'language')}<select value={me.language} onChange={e => upd({ language: e.target.value as Lang })}><option value="id">Indonesia</option><option value="en">English</option></select></label>
      <label>{t(l, 'theme')}<select value={me.theme} onChange={e => upd({ theme: e.target.value })}>{['light', 'dark', 'system'].map(x => <option key={x} value={x}>{t(l, x)}</option>)}</select></label>
      <label>{t(l, 'timezone')}<select value={me.timezone} onChange={e => upd({ timezone: e.target.value })}>{tzs.map(z => <option key={z}>{z}</option>)}</select></label>
    </div></div>
    <div className="card"><h3>{t(l, 'changePw')}</h3><ChangePw me={me} onDone={() => {}} /></div>
  </>
}

function Sources({ me, can }: { me: Me; can: (p: string) => boolean }) {
  const l = me.language
  const [rows, setRows] = useState<any[]>([]); const [msg, setMsg] = useState(''); const [show, setShow] = useState(false); const [q, setQ] = useState('')
  const load = () => api('GET', '/sources').then(setRows).catch(() => {})
  useEffect(() => { load() }, [])
  const empty = { platform: 'proxmox', name: '', baseUrl: 'https://', environment: '', pollIntervalSeconds: 1800, authType: 'token', tokenId: '', tokenSecret: '', username: '', password: '', tlsVerify: true, caPem: '' }
  const [f, setF] = useState<any>(empty)
  const [cred, setCred] = useState<any>(null)
  const [edit, setEdit] = useState<any>(null)
  const set = (k: string, v: any) => setF({ ...f, [k]: v })
  const run = async (fn: () => Promise<any>) => { setMsg('...'); try { const r = await fn(); if (r && r.ok === false) setMsg('✖ ' + r.error); else if (r && r.info && r.info.note) setMsg('✔ ' + r.info.note); else if (r && r.queued) setMsg('✔ ' + t(l, 'pollQueued')); else setMsg(r && r.ok ? '✔ ' + JSON.stringify(r) : t(l, 'saved')); load() } catch (x) { setMsg(x instanceof ApiError ? x.message : t(l, 'error')) } }
  const submit = async (ev: React.FormEvent) => {
    ev.preventDefault()
    const b: any = { name: f.name, platform: f.platform, baseUrl: f.baseUrl, environment: f.environment, pollIntervalSeconds: Number(f.pollIntervalSeconds), authType: f.authType, tlsVerify: f.tlsVerify, caPem: f.caPem }
    if (f.platform === 'proxmox' && f.authType === 'token') { b.tokenId = f.tokenId; b.tokenSecret = f.tokenSecret } else { b.username = f.username; b.password = f.password }
    await run(async () => { await api('POST', '/sources', b); setF(empty); setShow(false) })
  }
  const fl = rows.filter(r => has(q, [r.name, r.baseUrl, r.platform])); const pk = usePager(fl.length)
  return <>
    {msg && <div className="card" role="status" style={{ wordBreak: 'break-all' }}>{msg}</div>}
    {can('sources.create') && <div className="card">
      {!show ? <button className="pri" onClick={() => setShow(true)}>{t(l, 'addSource')}</button> :
        <form onSubmit={submit} autoComplete="off">
          <h3>{t(l, 'addSource')}</h3>
          <label>{t(l, 'platform')}<select value={f.platform} onChange={e => setF({ ...empty, platform: e.target.value, name: f.name, baseUrl: e.target.value === 'veeam' ? 'https://:9398' : e.target.value === 'synology_abb' ? 'https://:5001' : 'https://', tlsVerify: e.target.value === 'proxmox' })}><option value="proxmox">Proxmox VE</option><option value="veeam">Veeam Backup Enterprise Manager</option><option value="synology_abb">Synology Active Backup for Business</option></select></label>
          <label>{t(l, 'name')}<input value={f.name} onChange={e => set('name', e.target.value)} required maxLength={100} /></label>
          <label>URL<input value={f.baseUrl} onChange={e => set('baseUrl', e.target.value)} placeholder="https://pve.example.com:8006" required /></label>
          <label>{t(l, 'environment')}<input value={f.environment} onChange={e => set('environment', e.target.value)} /></label>
          <label>{t(l, 'interval')}<input type="number" min={300} max={86400} value={f.pollIntervalSeconds} onChange={e => set('pollIntervalSeconds', e.target.value)} /></label>
          {f.platform === 'proxmox' && <label>{t(l, 'authType')}<select value={f.authType} onChange={e => set('authType', e.target.value)}><option value="token">API token</option><option value="password">{t(l, 'userPass')}</option></select></label>}
          {f.platform === 'proxmox' && f.authType === 'token' ? <>
            <label>Token ID (user@realm!name)<input value={f.tokenId} onChange={e => set('tokenId', e.target.value)} placeholder="monitor@pve!backupmon" autoComplete="off" required /></label>
            <label>Token secret<input type="password" value={f.tokenSecret} onChange={e => set('tokenSecret', e.target.value)} autoComplete="new-password" required /></label>
          </> : <>
            <label>{t(l, 'username')} (user@realm)<input value={f.username} onChange={e => set('username', e.target.value)} placeholder={f.platform === 'proxmox' ? 'monitor@pve' : f.platform === 'veeam' ? 'DOMAIN\\\\user' : 'monitor'} autoComplete="off" required /></label>
            <label>{t(l, 'password')}<input type="password" value={f.password} onChange={e => set('password', e.target.value)} autoComplete="new-password" required /></label>
          </>}
          <label><input type="checkbox" checked={f.tlsVerify} onChange={e => set('tlsVerify', e.target.checked)} /> {t(l, 'tlsVerify')}</label>
          <label>{t(l, 'caPem')}<textarea value={f.caPem} onChange={e => set('caPem', e.target.value)} rows={3} /></label>
          <button className="pri">{t(l, 'save')}</button> <button type="button" onClick={() => { setShow(false); setF(empty) }}>{t(l, 'cancel')}</button>
        </form>}
    </div>}
    {edit && <div style={{position:'fixed', inset:0, background:'rgba(0,0,0,.55)', zIndex:9999, display:'flex', alignItems:'flex-start', justifyContent:'center', overflowY:'auto', padding:'40px 16px'}}><div className="card" style={{ border: '2px solid #4c8dff', maxWidth: 640, width: '100%' }}><form autoComplete="off" onSubmit={async ev => {
      ev.preventDefault()
      const b: any = { name: edit.name, baseUrl: edit.baseUrl, environment: edit.environment, pollIntervalSeconds: Number(edit.pollIntervalSeconds), tlsVerify: edit.tlsVerify, caPem: edit.caPem }
      if (edit.username || edit.password) { b.authType = 'password'; b.username = edit.username; b.password = edit.password }
      const id = edit.id; setEdit(null); await run(() => api('PATCH', `/sources/${id}`, b))
    }}>
      <h3>{t(l, 'editSource')}</h3>
      <label>{t(l, 'name')}<input value={edit.name} onChange={e => setEdit({ ...edit, name: e.target.value })} required maxLength={100} /></label>
      <label>URL<input value={edit.baseUrl} onChange={e => setEdit({ ...edit, baseUrl: e.target.value })} required /></label>
      <label>{t(l, 'environment')}<input value={edit.environment} onChange={e => setEdit({ ...edit, environment: e.target.value })} /></label>
      <label>{t(l, 'interval')}<input type="number" min={300} max={86400} value={edit.pollIntervalSeconds} onChange={e => setEdit({ ...edit, pollIntervalSeconds: e.target.value })} /></label>
      <label><input type="checkbox" checked={edit.tlsVerify} onChange={e => setEdit({ ...edit, tlsVerify: e.target.checked })} /> {t(l, 'tlsVerify')}</label>
      <label>{t(l, 'caPem')}<textarea value={edit.caPem} onChange={e => setEdit({ ...edit, caPem: e.target.value })} rows={3} /></label>
      {edit.platform !== 'proxmox' && <>
        <label>{t(l, 'username')} ({t(l, 'keepBlank')})<input value={edit.username} onChange={e => setEdit({ ...edit, username: e.target.value })} autoComplete="off" placeholder={edit.platform === 'veeam' ? 'DOMAIN\\user' : 'monitor'} /></label>
        <label>{t(l, 'password')} ({t(l, 'keepBlank')})<input type="password" value={edit.password} onChange={e => setEdit({ ...edit, password: e.target.value })} autoComplete="new-password" /></label>
      </>}
      <button className="pri">{t(l, 'save')}</button> <button type="button" onClick={() => setEdit(null)}>{t(l, 'cancel')}</button>
    </form></div></div>}
    {cred && <div style={{position:'fixed', inset:0, background:'rgba(0,0,0,.55)', zIndex:9999, display:'flex', alignItems:'flex-start', justifyContent:'center', overflowY:'auto', padding:'40px 16px'}}><div className="card" style={{ border: '2px solid #4c8dff', maxWidth: 640, width: '100%' }}><form autoComplete="off" onSubmit={async ev => { ev.preventDefault(); const b = { authType: 'password', username: cred.username, password: cred.password }; setCred(null); await run(() => api('PATCH', `/sources/${cred.id}`, b)) }}>
      <h3>{t(l, 'changeCred')}</h3>
      <label>{t(l, 'username')}<input value={cred.username} onChange={e => setCred({ ...cred, username: e.target.value })} autoComplete="off" required /></label>
      <label>{t(l, 'password')}<input type="password" value={cred.password} onChange={e => setCred({ ...cred, password: e.target.value })} autoComplete="new-password" required /></label>
      <button className="pri">{t(l, 'save')}</button> <button type="button" onClick={() => setCred(null)}>{t(l, 'cancel')}</button></form></div></div>}
    <div className="card" style={{ overflowX: 'auto' }}><SearchBox l={l} q={q} setQ={setQ} /><table><thead><tr><th>{t(l, 'name')}</th><th>URL</th><th>{t(l, 'platform')}</th><th>{t(l, 'status')}</th><th>{t(l, 'lastPoll')}</th><th>VM</th><th></th></tr></thead>
      <tbody>{fl.slice(pk.from, pk.to).map(r => <tr key={r.id}><td>{r.name}{!r.enabled && ` (${t(l, 'off')})`}</td><td>{r.baseUrl}</td><td>{r.platform}</td>
        <td>{r.connectionStatus}{r.lastError ? <div className="err">{r.lastError}</div> : null}</td><td>{fmt(r.lastPollAt, me.timezone, l)}</td><td>{r.workloads}</td>
        <td style={{ whiteSpace: 'nowrap' }}>
          {can('sources.test') && <><button onClick={() => run(() => api('POST', `/sources/${r.id}/test`))}>{t(l, 'test')}</button> {r.platform !== 'veeam_push' && <button onClick={() => run(() => api('POST', `/sources/${r.id}/poll`))}>{t(l, 'pollNow')}</button>} </>}
          {can('sources.update') && <button onClick={() => setEdit({ id: r.id, platform: r.platform, name: r.name, baseUrl: r.baseUrl, environment: r.environment || '', pollIntervalSeconds: r.pollIntervalSeconds || 1800, tlsVerify: r.tlsVerify !== false, caPem: r.caPem || '', username: '', password: '' })}>{t(l, 'edit')}</button>}{' '}
          {can('sources.update') && r.platform !== 'proxmox' && <><button onClick={() => setCred({ id: r.id, username: '', password: '' })}>{t(l, 'changeCred')}</button> </>}
          {can('sources.enable') && <button onClick={() => run(() => api('PATCH', `/sources/${r.id}/enabled`, { enabled: !r.enabled }))}>{r.enabled ? t(l, 'disable') : t(l, 'enable')}</button>}{' '}
          {can('sources.delete') && <button onClick={() => { if (confirm(t(l, 'confirmDel') + ' ' + r.name + '?')) run(() => api('DELETE', `/sources/${r.id}`)) }}>{t(l, 'remove')}</button>}
        </td></tr>)}</tbody></table>{rows.length === 0 && <p className="mut">{t(l, 'noSources')}</p>}<Pager l={l} pk={pk} /></div>
  </>
}

function Storage({ me }: { me: Me }) {
  const l = me.language; const [rows, setRows] = useState<any[]>([]); const [q, setQ] = useState(''); const [f, setF] = useState('')
  useEffect(() => { api('GET', '/storage').then(setRows).catch(() => {}) }, [])
  const gb = (b: number) => b >= 1e12 ? (b / 1e12).toFixed(2) + ' TB' : (b / 1e9).toFixed(1) + ' GB'
  const pct = (r: any) => r.totalBytes > 0 ? r.usedBytes / r.totalBytes * 100 : 0
  const st = (r: any) => !r.online ? 'offline' : pct(r) >= 95 ? 'critical' : pct(r) >= 85 ? 'warning' : 'healthy'
  const col: Record<string, string> = { healthy: '#22c55e', warning: '#f59e0b', critical: '#ef4444', offline: '#ef4444' }
  const fl = rows.filter(r => (!q || JSON.stringify([r.name, r.node, r.source, r.kind]).toLowerCase().includes(q.toLowerCase())) && (!f || st(r) === f))
  const cnt = (s: string) => rows.filter(r => st(r) === s).length
  const pk = usePager(fl.length)
  const cards: [string, number, string][] = [[t(l, 'stTotal'), rows.length, '#3b82f6'], [t(l, 'stHealthy'), cnt('healthy'), '#22c55e'], [t(l, 'stWarning'), cnt('warning'), '#f59e0b'], [t(l, 'stCritical'), cnt('critical') + cnt('offline'), '#ef4444']]
  return <>
    <div className="sumgrid">{cards.map(([k, v, c]) => <div key={k} className="sum"><div className="sum-n" style={{ color: c }}>{v}</div><div className="sum-l">{k}</div></div>)}</div>
    <div className="card" style={{ overflowX: 'auto' }}>
      <div style={{ display: 'flex', gap: 10, alignItems: 'center', marginBottom: 10 }}>
        <input placeholder={t(l, 'search')} value={q} onChange={x => setQ(x.target.value)} />
        <select value={f} onChange={x => setF(x.target.value)}><option value="">{t(l, 'all')}</option><option value="healthy">{t(l, 'stHealthy')}</option><option value="warning">{t(l, 'stWarning')}</option><option value="critical">{t(l, 'stCritical')}</option><option value="offline">offline</option></select>
        <span className="mut">{fl.length} / {rows.length}</span></div>
      <table><thead><tr><th>{t(l, 'stName')}</th><th>{t(l, 'stType')}</th><th>{t(l, 'stCap')}</th><th>{t(l, 'stFree')}</th><th>{t(l, 'status')}</th></tr></thead>
        <tbody>{fl.slice(pk.from, pk.to).map(r => { const p = pct(r), s = st(r); return <tr key={r.source + r.node + r.name}>
          <td><b>{r.name}</b><div className="mut">{r.shared ? t(l, 'shared') : r.node} · {r.source}</div></td><td>{r.kind}</td>
          <td style={{ minWidth: 200 }}><div style={{ height: 8, borderRadius: 4, background: 'rgba(128,128,128,.25)' }}><div style={{ width: Math.min(100, p) + '%', height: 8, borderRadius: 4, background: col[s] }} /></div>
            <div className="mut">{gb(r.usedBytes)} / {gb(r.totalBytes)} ({p.toFixed(1)}%)</div></td>
          <td>{gb(Math.max(0, r.totalBytes - r.usedBytes))}</td>
          <td><span className="pill" style={{ color: col[s] }}>{vkey(l, s).toUpperCase()}</span></td></tr> })}</tbody></table>
      <Pager l={l} pk={pk} />
      <p className="mut">{t(l, 'stNote')}</p>
    </div>
  </>
}

const cell = (l: Lang, typ: string, col: string, v: string) => {
  if (col === 'last_result') return vkey(l, v)
  if (v === 'shared') return t(l, 'val_shared')
  if (typ === 'veeam_notrun' && col === 'last_run' && v.startsWith('> 48h')) return t(l, 'val_out_window')
  return v
}

const PER_OPTS = [10, 30, 50, 100]
function usePager(n: number) {
  const [pg, setPg] = useState(0); const [per, setPer] = useState(10)
  useEffect(() => { setPg(0) }, [n, per])
  const pages = Math.max(1, Math.ceil(n / per)); const cur = Math.min(pg, pages - 1)
  return { n, per, setPer, pages, cur, setPg, from: cur * per, to: cur * per + per }
}
function Pager({ l, pk }: { l: Lang; pk: ReturnType<typeof usePager> }) {
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
const SearchBox = ({ l, q, setQ }: { l: Lang; q: string; setQ: (v: string) => void }) => <div style={{ display: 'flex', gap: 10, alignItems: 'center', marginBottom: 10 }}><input placeholder={t(l, 'search')} value={q} onChange={e => setQ(e.target.value)} style={{ maxWidth: 320 }} /></div>
const has = (q: string, v: any) => !q || JSON.stringify(v).toLowerCase().includes(q.toLowerCase())
const vkey = (l: Lang, v: string) => { const s = t(l, 'val_' + v); return s.startsWith('val_') ? v : s }

function Reports({ me }: { me: Me }) {
  const l = me.language; const [d, setD] = useState<any>(null); const [typ, setTyp] = useState('veeam_notrun'); const [data, setData] = useState<any>(null); const [q, setQ] = useState('')
  useEffect(() => { api('GET', '/dashboard/summary').then(setD).catch(() => {}) }, [])
  useEffect(() => { setData(null); api('GET', `/reports/data?type=${typ}`).then(setData).catch(() => {}) }, [typ])
  const p = d?.daily?.platforms
  const frows: string[][] = (data?.rows || []).filter((r: string[]) => has(q, r)); const pk = usePager(frows.length)
  const items: [string, string, string][] = [['veeam_notrun', t(l, 'rpVeeam'), String(d?.daily?.veeamNotRun ?? '-')], ['pve_missing', t(l, 'rpPve'), String(d?.daily?.pve?.missing ?? '-')],
    ['abb_merge', t(l, 'rpMerge'), String(p?.abb?.warning ?? '-')], ['abb_failed', t(l, 'rpAbbFail'), String(p?.abb?.failed ?? '-')], ['storage', t(l, 'navStorage'), '']]
  const sum: [string, any, string][] = p ? [['PVE', `${p.proxmox.success} OK / ${p.proxmox.failed} ${t(l, 'rpFail')}`, '#3b82f6'], ['NAS ABB', `${p.abb.success} OK / ${p.abb.warning} warn / ${p.abb.failed} ${t(l, 'rpFail')}`, '#f59e0b'], ['Veeam', `${p.veeam.success} OK / ${p.veeam.failed} ${t(l, 'rpFail')}`, '#22c55e']] : []
  return <>
    <div className="sumgrid">{sum.map(([k, v, c]) => <div key={k} className="sum"><div className="sum-n" style={{ color: c, fontSize: 18 }}>{v}</div><div className="sum-l">{k} · 24h</div></div>)}</div>
    <div className="card" style={{ overflowX: 'auto' }}>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center', marginBottom: 10 }}>
        {items.map(([k, lbl, n]) => <button key={k} className={typ === k ? 'pri' : ''} onClick={() => { setTyp(k); setQ('') }}>{lbl}{n !== '' ? ` (${n})` : ''}</button>)}
        <span className="tb-sp" /><a href={`/api/v1/reports/data?type=${typ}&format=csv`}><button className="pri">{t(l, 'rpCsv')}</button></a></div>
      <SearchBox l={l} q={q} setQ={setQ} />
      {!data ? <p className="mut">…</p> : <table><thead><tr>{data.columns.map((c: string) => <th key={c}>{t(l, 'rc_' + c)}</th>)}</tr></thead>
        <tbody>{frows.slice(pk.from, pk.to).map((r: string[], i: number) => <tr key={i}>{r.map((c, j) => <td key={j}>{cell(l, typ, data.columns[j], c)}</td>)}</tr>)}</tbody></table>}
      {data && data.rows.length === 0 && <p className="mut">{t(l, 'rpEmpty')}</p>}
      {data && <Pager l={l} pk={pk} />}
      <p className="mut">{t(l, 'rpNote')}</p>
    </div>
  </>
}

function Workloads({ me }: { me: Me }) {
  const l = me.language
  const [rows, setRows] = useState<any[]>([]); const [state, setState] = useState(''); const [q, setQ] = useState('')
  useEffect(() => { api('GET', `/workloads?state=${encodeURIComponent(state)}&q=${encodeURIComponent(q)}`).then(setRows).catch(() => {}) }, [state, q])
  const pk = usePager(rows.length)
  return <>
    <div className="card"><div className="grid">
      <label>{t(l, 'state')}<select value={state} onChange={e => setState(e.target.value)}><option value="">{t(l, 'all')}</option>{['failed', 'never', 'stale', 'ok', 'disabled'].map(s => <option key={s} value={s}>{t(l, s)}</option>)}</select></label>
      <label>{t(l, 'search')}<input value={q} onChange={e => setQ(e.target.value)} /></label>
      <div><div className="mut">{t(l, 'total')}</div><div className="k">{rows.length}</div></div>
    </div></div>
    <div className="card" style={{ overflowX: 'auto' }}><table><thead><tr><th>VMID</th><th>{t(l, 'name')}</th><th>{t(l, 'colNode')}</th><th>{t(l, 'colStatus')}</th><th>{t(l, 'lastSuccess')}</th><th>{t(l, 'abbNext')}</th><th>{t(l, 'state')}</th></tr></thead>
      <tbody>{rows.slice(pk.from, pk.to).map(r => <tr key={r.id}><td>{r.vmid}</td><td>{r.name}</td><td>{r.node}</td><td>{vkey(l, r.status)}</td><td>{r.lastSuccess ? fmt(r.lastSuccess, me.timezone, l) : (r.state === 'disabled' ? '—' : t(l, 'never'))}</td><td>{r.nextRun ? fmt(r.nextRun, me.timezone, l) : (r.kind === 'synology_abb' ? t(l, 'noSchedule') : '—')}</td><td>{t(l, r.state)}</td></tr>)}</tbody></table><Pager l={l} pk={pk} /></div>
    <AbbTasks me={me} />
  </>
}

function AbbTasks({ me }: { me: Me }) {
  const l = me.language
  const [rows, setRows] = useState<any[]>([]); const [q, setQ] = useState('')
  useEffect(() => { api('GET', '/abb/tasks').then(setRows).catch(() => {}) }, [])
  const fl = rows.filter(r => has(q, [r.name, r.devices, r.schedule, r.codes24h])); const pk = usePager(fl.length)
  if (!rows.length) return null
  return <div className="card" style={{ overflowX: 'auto' }}><h3>{t(l, 'abbTitle')} ({rows.length}; {t(l, 'abbSched')} {rows.filter(r => r.nextRun).length})</h3>
    <SearchBox l={l} q={q} setQ={setQ} />
    <table><thead><tr><th>{t(l, 'abbTask')}</th><th>VM</th><th>{t(l, 'abbSchedule')}</th><th>{t(l, 'abbNext')}</th><th>{t(l, 'abbLast')}</th><th>{t(l, 'abbErr')}</th></tr></thead>
      <tbody>{fl.slice(pk.from, pk.to).map(r => <tr key={r.sourceName + r.taskId}><td>{r.name}</td><td>{r.devices}</td><td>{r.nextRun ? (t(l, 'sch_' + r.schedule).startsWith('sch_') ? r.schedule : t(l, 'sch_' + r.schedule)) : t(l, 'noSchedule')}</td><td>{r.nextRun ? fmt(r.nextRun, me.timezone, me.language) : '—'}</td><td>{r.lastEnd ? fmt(r.lastEnd, me.timezone, me.language) : '—'}</td><td>{r.codes24h || ''}</td></tr>)}</tbody></table><Pager l={l} pk={pk} /></div>
}
