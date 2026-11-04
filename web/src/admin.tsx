import { useCallback, useEffect, useState } from 'react'
import { api, ApiError } from './api'
import { Lang } from './i18n'
import { type Me, fmt, usePager, Pager, SearchBox, has } from './common'

const L = (l: Lang, id: string, en: string) => (l === 'id' ? id : en)
const errMsg = (x: unknown, l: Lang) => (x instanceof ApiError ? x.message : L(l, 'Terjadi kesalahan', 'Something went wrong'))
const SEV: Record<string, string> = { critical: '#ef4444', warning: '#f59e0b', ok: '#22c55e' }
type Can = (p: string) => boolean

function Msg({ m }: { m: { ok: boolean; text: string } | null }) {
  return m ? <div role="status" className={m.ok ? 'mut' : 'err'} style={{ margin: '8px 0' }}>{m.text}</div> : null
}

// ===================================================================== Alerts

export function Alerts({ me, can }: { me: Me; can: Can }) {
  const l = me.language
  const [tab, setTab] = useState<'alerts' | 'channels' | 'rules'>('alerts')
  const cfg = can('alerts.configure')
  const tabs: [typeof tab, string][] = [['alerts', L(l, 'Alert', 'Alerts')]]
  if (cfg) tabs.push(['channels', L(l, 'Kanal notifikasi', 'Notification channels')], ['rules', L(l, 'Aturan', 'Rules')])
  return <>
    <div className="tabs">{tabs.map(([k, label]) => <button key={k} className={tab === k ? 'on' : ''} onClick={() => setTab(k)}>{label}</button>)}</div>
    {tab === 'alerts' && <AlertList me={me} can={can} />}
    {tab === 'channels' && cfg && <Channels me={me} />}
    {tab === 'rules' && cfg && <AlertRules me={me} />}
  </>
}

function AlertList({ me, can }: { me: Me; can: Can }) {
  const l = me.language
  const [filter, setFilter] = useState('active')
  const [rows, setRows] = useState<any[]>([])
  const [q, setQ] = useState('')
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null)
  const load = useCallback(() => { api<any[]>('GET', `/alerts?state=${filter}&limit=500`).then(setRows).catch(() => {}) }, [filter])
  useEffect(() => { load(); const i = setInterval(load, 60000); return () => clearInterval(i) }, [load])
  const act = async (id: number, what: 'acknowledge' | 'resolve') => {
    setMsg(null)
    try { await api('POST', `/alerts/${id}/${what}`); load() } catch (x) { setMsg({ ok: false, text: errMsg(x, l) }) }
  }
  const n = (f: (r: any) => boolean) => rows.filter(f).length
  const cards: [string, number, string][] = [
    [L(l, 'Kritis', 'Critical'), n(r => r.state === 'open' && r.severity === 'critical'), SEV.critical],
    [L(l, 'Peringatan', 'Warning'), n(r => r.state === 'open' && r.severity === 'warning'), SEV.warning],
    [L(l, 'Diakui', 'Acknowledged'), n(r => r.state === 'acknowledged'), '#4f8ff7'],
  ]
  const fl = rows.filter(r => has(q, [r.title, r.detail, r.source, r.type]))
  const pk = usePager(fl.length)
  const stateLbl = (s: string) => s === 'open' ? L(l, 'terbuka', 'open') : s === 'acknowledged' ? L(l, 'diakui', 'acknowledged') : L(l, 'selesai', 'resolved')
  return <>
    <div className="sumgrid">{cards.map(([k, v, c]) => <div key={k} className="sum"><div className="sum-n" style={{ color: c }}>{v}</div><div className="sum-l">{k}</div></div>)}</div>
    <div className="card" style={{ overflowX: 'auto' }}>
      <div style={{ display: 'flex', gap: 10, alignItems: 'center', marginBottom: 10, flexWrap: 'wrap' }}>
        <SearchBox l={l} q={q} setQ={setQ} />
        <select value={filter} onChange={e => setFilter(e.target.value)} style={{ width: 'auto' }}>
          <option value="active">{L(l, 'Aktif (terbuka + diakui)', 'Active (open + acknowledged)')}</option>
          <option value="open">{L(l, 'Terbuka', 'Open')}</option>
          <option value="acknowledged">{L(l, 'Diakui', 'Acknowledged')}</option>
          <option value="resolved">{L(l, 'Selesai', 'Resolved')}</option>
          <option value="all">{L(l, 'Semua', 'All')}</option>
        </select>
      </div>
      <Msg m={msg} />
      <table><thead><tr><th>{L(l, 'Tingkat', 'Severity')}</th><th>{L(l, 'Alert', 'Alert')}</th><th>{L(l, 'Pertama', 'First seen')}</th><th>{L(l, 'Terakhir', 'Last seen')}</th><th>{L(l, 'Status', 'State')}</th><th /></tr></thead>
        <tbody>{fl.slice(pk.from, pk.to).map(r => <tr key={r.id}>
          <td><span className="pill" style={{ color: SEV[r.severity] }}>{r.severity === 'critical' ? L(l, 'kritis', 'critical') : L(l, 'peringatan', 'warning')}</span></td>
          <td>{r.title}{r.detail && <div className="mut" style={{ fontSize: 11 }}>{r.detail}</div>}</td>
          <td>{fmt(r.firstSeen, me.timezone, l)}</td><td>{fmt(r.lastSeen, me.timezone, l)}</td>
          <td><span className="pill">{stateLbl(r.state)}</span>{r.acknowledgedBy && <div className="mut" style={{ fontSize: 11 }}>{r.acknowledgedBy}</div>}</td>
          <td className="row-actions">
            {r.state === 'open' && can('alerts.acknowledge') && <button onClick={() => act(r.id, 'acknowledge')}>{L(l, 'Akui', 'Acknowledge')}</button>}
            {r.state !== 'resolved' && can('alerts.resolve') && <button onClick={() => act(r.id, 'resolve')} title={L(l, 'Alert akan muncul lagi bila kondisinya masih ada', 'The alert reappears if the condition persists')}>{L(l, 'Tutup', 'Resolve')}</button>}
          </td></tr>)}
          {fl.length === 0 && <tr><td colSpan={6} className="mut">{L(l, 'Tidak ada alert.', 'No alerts.')}</td></tr>}</tbody></table>
      <Pager l={l} pk={pk} />
      <p className="mut" style={{ fontSize: 11 }}>{L(l, '“Akui” menghentikan notifikasi ulang. “Tutup” hanya menyelesaikan sementara: alert dibuka lagi bila kondisinya masih ada.',
        '“Acknowledge” stops repeat notifications. “Resolve” is temporary: the alert reopens if the condition persists.')}</p>
    </div>
  </>
}

type ChForm = { kind: string; name: string; enabled: boolean; host: string; port: string; security: string; username: string; password: string; from: string; to: string; botToken: string; chatId: string; url: string; authorization: string }
const emptyCh: ChForm = { kind: 'webhook', name: '', enabled: true, host: '', port: '', security: 'starttls', username: '', password: '', from: '', to: '', botToken: '', chatId: '', url: '', authorization: '' }

function Channels({ me }: { me: Me }) {
  const l = me.language
  const [rows, setRows] = useState<any[]>([])
  const [f, setF] = useState<ChForm | null>(null)
  const [editId, setEditId] = useState<string | null>(null)
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null)
  const load = () => api<any[]>('GET', '/notification-channels').then(setRows).catch(() => {})
  useEffect(() => { load() }, [])
  const set = (k: keyof ChForm, v: string | boolean) => setF(p => p && { ...p, [k]: v })
  const edit = (r: any) => {
    const c = r.config || {}
    setEditId(r.id); setMsg(null)
    setF({ ...emptyCh, kind: r.kind, name: r.name, enabled: r.enabled, host: c.host || '', port: c.port ? String(c.port) : '', security: c.security || 'starttls', username: c.username || '', from: c.from || '', to: (c.to || []).join(', '), chatId: c.chatId || '' })
  }
  const submit = async (ev: React.FormEvent) => {
    ev.preventDefault(); if (!f) return
    setMsg(null)
    const body: Record<string, unknown> = { name: f.name, enabled: f.enabled }
    if (!editId) body.kind = f.kind
    if (f.kind === 'email') Object.assign(body, { host: f.host, security: f.security, username: f.username, from: f.from, to: f.to.split(',').map(s => s.trim()).filter(Boolean), ...(f.port ? { port: Number(f.port) } : {}), ...(f.password ? { password: f.password } : {}) })
    if (f.kind === 'telegram') Object.assign(body, { chatId: f.chatId, ...(f.botToken ? { botToken: f.botToken } : {}) })
    if (f.kind === 'webhook') Object.assign(body, { ...(f.url ? { url: f.url } : {}), ...(f.authorization ? { authorization: f.authorization } : {}) })
    try {
      if (editId) await api('PATCH', `/notification-channels/${editId}`, body); else await api('POST', '/notification-channels', body)
      setF(null); setEditId(null); load()
    } catch (x) { setMsg({ ok: false, text: errMsg(x, l) }) }
  }
  const test = async (r: any) => {
    setMsg(null)
    try {
      const j = await api<{ ok: boolean; error?: string }>('POST', `/notification-channels/${r.id}/test`)
      setMsg({ ok: j.ok, text: j.ok ? L(l, `Tes ke “${r.name}” berhasil.`, `Test to “${r.name}” succeeded.`) : `${r.name}: ${j.error}` }); load()
    } catch (x) { setMsg({ ok: false, text: errMsg(x, l) }) }
  }
  const toggle = async (r: any) => { try { await api('PATCH', `/notification-channels/${r.id}`, { enabled: !r.enabled }); load() } catch (x) { setMsg({ ok: false, text: errMsg(x, l) }) } }
  const del = async (r: any) => {
    if (!confirm(L(l, `Hapus kanal “${r.name}”?`, `Delete channel “${r.name}”?`))) return
    try { await api('DELETE', `/notification-channels/${r.id}`); load() } catch (x) { setMsg({ ok: false, text: errMsg(x, l) }) }
  }
  const dest = (r: any) => {
    const c = r.config || {}
    if (r.kind === 'email') return `${c.host}:${c.port} → ${(c.to || []).join(', ')}`
    if (r.kind === 'telegram') return `chat ${c.chatId}`
    return c.url || ''
  }
  const keep = editId ? L(l, '(kosongkan = tidak diubah)', '(blank = unchanged)') : ''
  return <>
    <div className="card" style={{ overflowX: 'auto' }}>
      <div style={{ display: 'flex', alignItems: 'center', marginBottom: 10 }}><h3 style={{ margin: 0, flex: 1 }}>{L(l, 'Kanal notifikasi', 'Notification channels')}</h3>
        <button className="pri" onClick={() => { setEditId(null); setF({ ...emptyCh }); setMsg(null) }}>{L(l, 'Tambah kanal', 'Add channel')}</button></div>
      <Msg m={msg} />
      <table><thead><tr><th>{L(l, 'Nama', 'Name')}</th><th>{L(l, 'Jenis', 'Type')}</th><th>{L(l, 'Tujuan', 'Destination')}</th><th>{L(l, 'Status terakhir', 'Last status')}</th><th /></tr></thead>
        <tbody>{rows.map(r => <tr key={r.id}>
          <td><span className={'tg ' + (r.enabled ? 'on' : 'off')} title={r.enabled ? 'on' : 'off'} /> {r.name}</td>
          <td><span className="pill">{r.kind}</span></td><td style={{ wordBreak: 'break-all' }}>{dest(r)}</td>
          <td><span className="pill" style={{ color: r.lastStatus === 'ok' ? SEV.ok : r.lastStatus === 'error' ? SEV.critical : undefined }}>{r.lastStatus}</span>
            {r.lastSentAt && <div className="mut" style={{ fontSize: 11 }}>{fmt(r.lastSentAt, me.timezone, l)}</div>}
            {r.lastError && <div className="err" style={{ fontSize: 11 }}>{r.lastError}</div>}</td>
          <td className="row-actions"><button onClick={() => test(r)}>{L(l, 'Tes', 'Test')}</button><button onClick={() => edit(r)}>{L(l, 'Ubah', 'Edit')}</button>
            <button onClick={() => toggle(r)}>{r.enabled ? L(l, 'Nonaktifkan', 'Disable') : L(l, 'Aktifkan', 'Enable')}</button><button onClick={() => del(r)}>{L(l, 'Hapus', 'Delete')}</button></td></tr>)}
          {rows.length === 0 && <tr><td colSpan={5} className="mut">{L(l, 'Belum ada kanal. Tanpa kanal, alert hanya tampil di halaman ini.', 'No channels yet. Without a channel, alerts are only shown on this page.')}</td></tr>}</tbody></table>
    </div>
    {f && <div className="card"><h3>{editId ? L(l, 'Ubah kanal', 'Edit channel') : L(l, 'Kanal baru', 'New channel')}</h3>
      <form className="wide" onSubmit={submit}><div className="grid">
        <label>{L(l, 'Nama', 'Name')}<input value={f.name} onChange={e => set('name', e.target.value)} required maxLength={100} /></label>
        <label>{L(l, 'Jenis', 'Type')}<select value={f.kind} disabled={!!editId} onChange={e => set('kind', e.target.value)}><option value="webhook">Webhook (Teams / Slack / lainnya)</option><option value="telegram">Telegram</option><option value="email">Email (SMTP)</option></select></label>
        {f.kind === 'webhook' && <>
          <label>URL (https){editId && <span className="mut"> {keep}</span>}<input value={f.url} onChange={e => set('url', e.target.value)} placeholder="https://…" required={!editId} autoComplete="off" /></label>
          <label>Authorization <span className="mut">{L(l, '(opsional, mis. Bearer …)', '(optional, e.g. Bearer …)')} {keep}</span><input type="password" value={f.authorization} onChange={e => set('authorization', e.target.value)} autoComplete="new-password" /></label></>}
        {f.kind === 'telegram' && <>
          <label>Bot token {keep}<input type="password" value={f.botToken} onChange={e => set('botToken', e.target.value)} required={!editId} autoComplete="new-password" /></label>
          <label>Chat ID<input value={f.chatId} onChange={e => set('chatId', e.target.value)} required /></label></>}
        {f.kind === 'email' && <>
          <label>SMTP host<input value={f.host} onChange={e => set('host', e.target.value)} required /></label>
          <label>Port <span className="mut">{L(l, '(kosong = otomatis)', '(blank = default)')}</span><input type="number" min={1} max={65535} value={f.port} onChange={e => set('port', e.target.value)} /></label>
          <label>{L(l, 'Keamanan', 'Security')}<select value={f.security} onChange={e => set('security', e.target.value)}><option value="starttls">STARTTLS (587)</option><option value="tls">TLS (465)</option><option value="none">{L(l, 'Tanpa (relay internal)', 'None (internal relay)')}</option></select></label>
          <label>{L(l, 'Pengguna SMTP', 'SMTP user')} <span className="mut">{L(l, '(opsional)', '(optional)')}</span><input value={f.username} onChange={e => set('username', e.target.value)} autoComplete="off" /></label>
          <label>{L(l, 'Password SMTP', 'SMTP password')} {keep}<input type="password" value={f.password} onChange={e => set('password', e.target.value)} autoComplete="new-password" /></label>
          <label>{L(l, 'Pengirim (From)', 'From')}<input type="email" value={f.from} onChange={e => set('from', e.target.value)} required /></label>
          <label>{L(l, 'Penerima (pisahkan koma)', 'Recipients (comma separated)')}<input value={f.to} onChange={e => set('to', e.target.value)} required /></label></>}
      </div>
        <label style={{ display: 'flex', gap: 8, alignItems: 'center' }}><input type="checkbox" checked={f.enabled} onChange={e => set('enabled', e.target.checked)} style={{ width: 'auto' }} />{L(l, 'Aktif', 'Enabled')}</label>
        <div style={{ display: 'flex', gap: 8 }}><button className="pri">{L(l, 'Simpan', 'Save')}</button><button type="button" onClick={() => { setF(null); setEditId(null) }}>{L(l, 'Batal', 'Cancel')}</button></div>
        <p className="mut" style={{ fontSize: 11 }}>{L(l, 'Kredensial disimpan terenkripsi dan tidak pernah ditampilkan kembali.', 'Credentials are stored encrypted and are never shown again.')}</p>
      </form></div>}
  </>
}

function AlertRules({ me }: { me: Me }) {
  const l = me.language
  const [s, setS] = useState<any>(null)
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null)
  useEffect(() => { api('GET', '/settings/alerts').then(setS).catch(() => {}) }, [])
  if (!s) return null
  const num = (k: string) => (e: React.ChangeEvent<HTMLInputElement>) => setS({ ...s, [k]: Number(e.target.value) })
  const chk = (k: string) => (e: React.ChangeEvent<HTMLInputElement>) => setS({ ...s, [k]: e.target.checked })
  const save = async (ev: React.FormEvent) => {
    ev.preventDefault(); setMsg(null)
    try { setS(await api('PUT', '/settings/alerts', s)); setMsg({ ok: true, text: L(l, 'Tersimpan', 'Saved') }) } catch (x) { setMsg({ ok: false, text: errMsg(x, l) }) }
  }
  const cb = (k: string, label: string) => <label style={{ display: 'flex', gap: 8, alignItems: 'center' }}><input type="checkbox" checked={!!s[k]} onChange={chk(k)} style={{ width: 'auto' }} />{label}</label>
  return <div className="card"><h3>{L(l, 'Aturan alert', 'Alert rules')}</h3>
    <form className="wide" onSubmit={save}>
      {cb('enabled', L(l, 'Aktifkan evaluasi alert (setiap 5 menit)', 'Enable alert evaluation (every 5 minutes)'))}
      <div className="grid">
        <label>{L(l, 'Backup dianggap basi setelah (jam)', 'Backup is stale after (hours)')}<input type="number" min={1} max={8760} value={s.staleHours} onChange={num('staleHours')} /></label>
        <label>{L(l, 'Ambang storage penuh (%)', 'Storage full threshold (%)')}<input type="number" min={50} max={99} value={s.storagePct} onChange={num('storagePct')} /></label>
        <label>{L(l, 'Ingatkan ulang tiap (jam, 0 = tidak)', 'Remind every (hours, 0 = never)')}<input type="number" min={0} max={720} value={s.repeatHours} onChange={num('repeatHours')} /></label>
      </div>
      {cb('includeNever', L(l, 'Beri alert juga untuk workload yang belum pernah berhasil di-backup', 'Also alert on workloads that never had a successful backup'))}
      {cb('notifyRecovery', L(l, 'Kirim notifikasi saat alert pulih', 'Send a notification when an alert recovers'))}
      <Msg m={msg} /><button className="pri">{L(l, 'Simpan', 'Save')}</button>
      <p className="mut" style={{ fontSize: 11 }}>{L(l, 'Alert dibuat untuk: backup gagal, backup basi, storage hampir penuh (≥ ambang; ≥ 95% kritis), dan sumber tidak terjangkau.',
        'Alerts are raised for: failed backups, stale backups, storage nearly full (≥ threshold; ≥ 95% is critical) and unreachable sources.')}</p>
    </form></div>
}

// ===================================================================== Users

const ROLE_COLORS: Record<string, string> = { super_admin: '#ef4444', administrator: '#f59e0b', backup_operator: '#4f8ff7', auditor: '#22c55e' }
const roleDesc = (l: Lang): Record<string, string> => ({
  super_admin: L(l, 'Akses penuh', 'Full access'),
  administrator: L(l, 'Kelola sumber, alert, laporan, setelan', 'Manage sources, alerts, reports, settings'),
  backup_operator: L(l, 'Lihat data, akui alert, ekspor laporan', 'View data, acknowledge alerts, export reports'),
  auditor: L(l, 'Baca saja termasuk audit log', 'Read only incl. audit log'),
})

function genPassword(): string {
  const a = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789'
  const b = new Uint32Array(16); crypto.getRandomValues(b)
  return Array.from(b, x => a[x % a.length]).join('')
}

export function UsersPage({ me, can }: { me: Me; can: Can }) {
  const l = me.language
  const [rows, setRows] = useState<any[]>([])
  const [roles, setRoles] = useState<string[]>(Object.keys(ROLE_COLORS))
  const [q, setQ] = useState('')
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null)
  const [temp, setTemp] = useState<{ user: string; pw: string } | null>(null)
  const [add, setAdd] = useState<{ username: string; displayName: string; role: string; password: string } | null>(null)
  const [edit, setEdit] = useState<{ id: string; username: string; displayName: string; role: string } | null>(null)
  const load = useCallback(() => { api<any[]>('GET', '/users').then(setRows).catch(() => {}) }, [])
  useEffect(() => { load(); if (can('roles.read')) api<any[]>('GET', '/roles').then(r => setRoles(r.map(x => x.name))).catch(() => {}) }, [load])
  const run = async (fn: () => Promise<unknown>, ok?: string) => {
    setMsg(null)
    try { await fn(); if (ok) setMsg({ ok: true, text: ok }); load() } catch (x) { setMsg({ ok: false, text: errMsg(x, l) }) }
  }
  const canEdit = can('users.update')
  const desc = roleDesc(l)
  const cnt = (r: string) => rows.filter(x => x.role === r).length
  const fl = rows.filter(r => has(q, [r.username, r.displayName, r.role]))
  const pk = usePager(fl.length)
  const reset = async (r: any) => {
    if (!confirm(L(l, `Buat password sementara untuk “${r.username}”? Semua sesinya akan dikeluarkan.`, `Generate a temporary password for “${r.username}”? All their sessions will be signed out.`))) return
    setMsg(null); setTemp(null)
    try { const j = await api<{ temporaryPassword: string }>('POST', `/users/${r.id}/reset-password`); setTemp({ user: r.username, pw: j.temporaryPassword }); load() } catch (x) { setMsg({ ok: false, text: errMsg(x, l) }) }
  }
  return <>
    <div className="rolegrid">{roles.map(r => <div key={r} className="rolecard" style={{ borderTop: '2px solid ' + (ROLE_COLORS[r] || '#8b5cf6') }}>
      <div className="rc-h"><b style={{ color: ROLE_COLORS[r] || '#8b5cf6' }}>{r}</b><span className="rc-n">{cnt(r)}</span></div><div className="rc-d">{desc[r] || ''}</div></div>)}</div>
    {temp && <div className="card notice"><b>{L(l, 'Password sementara untuk', 'Temporary password for')} {temp.user}</b>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', margin: '8px 0' }}><code style={{ fontSize: 16, userSelect: 'all' }}>{temp.pw}</code>
        <button onClick={() => navigator.clipboard?.writeText(temp.pw)}>{L(l, 'Salin', 'Copy')}</button><button onClick={() => setTemp(null)}>{L(l, 'Tutup', 'Dismiss')}</button></div>
      <span className="mut" style={{ fontSize: 11 }}>{L(l, 'Hanya ditampilkan sekali. Pengguna wajib menggantinya saat login berikutnya.', 'Shown only once. The user must change it at next sign-in.')}</span></div>}
    <div className="card" style={{ overflowX: 'auto' }}>
      <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}><SearchBox l={l} q={q} setQ={setQ} /><span style={{ flex: 1 }} />
        {can('users.create') && <button className="pri" onClick={() => { setAdd({ username: '', displayName: '', role: 'backup_operator', password: genPassword() }); setMsg(null) }}>{L(l, 'Tambah pengguna', 'Add user')}</button>}</div>
      <Msg m={msg} />
      <table><thead><tr><th>{L(l, 'Pengguna', 'Username')}</th><th>{L(l, 'Peran', 'Role')}</th><th>{L(l, 'Status', 'Status')}</th><th>{L(l, 'Login terakhir', 'Last sign-in')}</th><th>{L(l, 'Sesi', 'Sessions')}</th>{(canEdit || can('users.delete')) && <th />}</tr></thead>
        <tbody>{fl.slice(pk.from, pk.to).map(r => { const self = r.id === me.id; return <tr key={r.id}>
          <td>{r.username}{self && <span className="mut"> ({L(l, 'Anda', 'you')})</span>}{r.displayName && <div className="mut" style={{ fontSize: 11 }}>{r.displayName}</div>}</td>
          <td><span className="pill" style={{ color: ROLE_COLORS[r.role] || '#8b5cf6' }}>{r.role}</span></td>
          <td><span className={'tg ' + (r.active ? 'on' : 'off')} /> {r.active ? L(l, 'Aktif', 'Active') : L(l, 'Nonaktif', 'Disabled')}{r.mustChangePassword && <div className="mut" style={{ fontSize: 11 }}>{L(l, 'wajib ganti password', 'must change password')}</div>}</td>
          <td>{fmt(r.lastLogin, me.timezone, l)}</td><td>{r.activeSessions}</td>
          {(canEdit || can('users.delete')) && <td className="row-actions">
            {canEdit && <button onClick={() => setEdit({ id: r.id, username: r.username, displayName: r.displayName, role: r.role })}>{L(l, 'Ubah', 'Edit')}</button>}
            {!self && canEdit && <button onClick={() => reset(r)}>{L(l, 'Reset password', 'Reset password')}</button>}
            {!self && canEdit && r.activeSessions > 0 && <button onClick={() => run(() => api('DELETE', `/users/${r.id}/sessions`), L(l, 'Sesi dicabut', 'Sessions revoked'))}>{L(l, 'Cabut sesi', 'Revoke sessions')}</button>}
            {!self && r.active && can('users.disable') && <button onClick={() => confirm(L(l, `Nonaktifkan “${r.username}”?`, `Disable “${r.username}”?`)) && run(() => api('PATCH', `/users/${r.id}/disable`))}>{L(l, 'Nonaktifkan', 'Disable')}</button>}
            {!self && !r.active && canEdit && <button onClick={() => run(() => api('PATCH', `/users/${r.id}/enable`))}>{L(l, 'Aktifkan', 'Enable')}</button>}
            {!self && !r.active && can('users.delete') && <button onClick={() => confirm(L(l, `Hapus permanen “${r.username}”?`, `Permanently delete “${r.username}”?`)) && run(() => api('DELETE', `/users/${r.id}`))}>{L(l, 'Hapus', 'Delete')}</button>}
          </td>}</tr> })}</tbody></table>
      <Pager l={l} pk={pk} />
    </div>
    {add && <div className="card"><h3>{L(l, 'Pengguna baru', 'New user')}</h3>
      <form className="wide" onSubmit={async ev => { ev.preventDefault(); setMsg(null); try { await api('POST', '/users', add); setAdd(null); setMsg({ ok: true, text: L(l, 'Pengguna dibuat. Sampaikan password awalnya; pengguna wajib menggantinya saat login.', 'User created. Share the initial password; the user must change it at first sign-in.') }); load() } catch (x) { setMsg({ ok: false, text: errMsg(x, l) }) } }}>
        <div className="grid">
          <label>{L(l, 'Username (huruf kecil, angka, . _ -)', 'Username (lowercase, digits, . _ -)')}<input value={add.username} onChange={e => setAdd({ ...add, username: e.target.value.toLowerCase() })} required pattern="[a-z0-9][a-z0-9._-]{2,63}" /></label>
          <label>{L(l, 'Nama tampilan', 'Display name')}<input value={add.displayName} onChange={e => setAdd({ ...add, displayName: e.target.value })} maxLength={100} /></label>
          <label>{L(l, 'Peran', 'Role')}<select value={add.role} onChange={e => setAdd({ ...add, role: e.target.value })}>{roles.map(r => <option key={r}>{r}</option>)}</select></label>
          <label>{L(l, 'Password awal (min. 12)', 'Initial password (min. 12)')}<input value={add.password} onChange={e => setAdd({ ...add, password: e.target.value })} minLength={12} required autoComplete="off" style={{ fontFamily: 'ui-monospace,monospace' }} /></label>
        </div>
        <div style={{ display: 'flex', gap: 8 }}><button className="pri">{L(l, 'Buat', 'Create')}</button><button type="button" onClick={() => setAdd({ ...add, password: genPassword() })}>{L(l, 'Buat password acak', 'Generate password')}</button><button type="button" onClick={() => setAdd(null)}>{L(l, 'Batal', 'Cancel')}</button></div>
      </form></div>}
    {edit && <div className="card"><h3>{L(l, 'Ubah pengguna', 'Edit user')}: {edit.username}</h3>
      <form className="wide" onSubmit={async ev => { ev.preventDefault(); const orig = rows.find(x => x.id === edit.id); await run(() => api('PATCH', `/users/${edit.id}`, { displayName: edit.displayName, ...(orig && orig.role !== edit.role ? { role: edit.role } : {}) }), L(l, 'Tersimpan', 'Saved')); setEdit(null) }}>
        <div className="grid">
          <label>{L(l, 'Nama tampilan', 'Display name')}<input value={edit.displayName} onChange={e => setEdit({ ...edit, displayName: e.target.value })} maxLength={100} /></label>
          <label>{L(l, 'Peran', 'Role')}<select value={edit.role} disabled={edit.id === me.id} onChange={e => setEdit({ ...edit, role: e.target.value })}>{roles.map(r => <option key={r}>{r}</option>)}</select></label>
        </div>
        {edit.id === me.id && <span className="mut" style={{ fontSize: 11 }}>{L(l, 'Anda tidak dapat mengubah peran akun sendiri.', 'You cannot change your own role.')}</span>}
        <div style={{ display: 'flex', gap: 8 }}><button className="pri">{L(l, 'Simpan', 'Save')}</button><button type="button" onClick={() => setEdit(null)}>{L(l, 'Batal', 'Cancel')}</button></div>
      </form></div>}
  </>
}

// ===================================================================== Settings (retention)

const RET: [string, string, string, string][] = [
  ['auditDays', 'Audit log', 'Audit log', 'auditLogs'],
  ['sessionsDays', 'Riwayat sesi backup', 'Backup session history', 'backupSessions'],
  ['abbEventDays', 'Event Synology ABB', 'Synology ABB events', 'abbEvents'],
  ['pollRunsDays', 'Riwayat polling sumber', 'Source poll history', 'pollRuns'],
  ['alertsDays', 'Alert yang sudah selesai', 'Resolved alerts', 'alerts'],
]

export function SettingsPage({ me, can }: { me: Me; can: Can }) {
  const l = me.language
  const [d, setD] = useState<any>(null)
  const [s, setS] = useState<Record<string, number>>({})
  const [prev, setPrev] = useState<Record<string, number> | null>(null)
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null)
  const edit = can('settings.update')
  const load = useCallback(() => api<any>('GET', '/settings/retention').then(j => { setD(j); setS(j.settings) }).catch(() => {}), [])
  useEffect(() => { load() }, [load])
  if (!d) return null
  const save = async (ev: React.FormEvent) => {
    ev.preventDefault(); setMsg(null); setPrev(null)
    try { await api('PUT', '/settings/retention', s); setMsg({ ok: true, text: L(l, 'Tersimpan. Pembersihan berjalan otomatis sekali sehari.', 'Saved. The purge runs automatically once a day.') }); load() } catch (x) { setMsg({ ok: false, text: errMsg(x, l) }) }
  }
  const dry = async () => { setMsg(null); try { setPrev((await api<any>('POST', '/settings/retention/run?dryRun=true')).deleted) } catch (x) { setMsg({ ok: false, text: errMsg(x, l) }) } }
  const run = async () => {
    const n = prev ? Object.values(prev).reduce((a, b) => a + b, 0) : null
    if (!confirm(L(l, `Hapus data yang melewati masa simpan${n !== null ? ` (${n} baris)` : ''}? Tindakan ini tidak dapat dibatalkan.`, `Delete data past its retention${n !== null ? ` (${n} rows)` : ''}? This cannot be undone.`))) return
    setMsg(null)
    try { const j = await api<any>('POST', '/settings/retention/run'); setPrev(null); setMsg({ ok: true, text: L(l, 'Selesai: ', 'Done: ') + Object.entries(j.deleted).map(([k, v]) => `${k}=${v}`).join(', ') }); load() } catch (x) { setMsg({ ok: false, text: errMsg(x, l) }) }
  }
  const last = d.lastRun
  return <div className="card"><h3>{L(l, 'Retensi data', 'Data retention')}</h3>
    <p className="mut">{L(l, 'Atur berapa lama tiap jenis data disimpan. Isi 0 untuk menyimpan selamanya. Pembersihan berjalan otomatis sekali sehari dan tercatat di audit log.',
      'Choose how long each kind of data is kept. Enter 0 to keep forever. The purge runs automatically once a day and is recorded in the audit log.')}</p>
    <form className="wide" onSubmit={save}>
      <table><thead><tr><th>{L(l, 'Data', 'Data')}</th><th>{L(l, 'Simpan (hari)', 'Keep (days)')}</th><th>{L(l, 'Minimum', 'Minimum')}</th><th>{L(l, 'Baris saat ini', 'Current rows')}</th><th>{L(l, 'Akan dihapus', 'Would delete')}</th></tr></thead>
        <tbody>{RET.map(([k, id, en, rk]) => <tr key={k}><td>{L(l, id, en)}</td>
          <td><input type="number" min={0} max={3650} value={s[k] ?? 0} disabled={!edit} onChange={e => setS({ ...s, [k]: Number(e.target.value) })} style={{ width: 100 }} /></td>
          <td className="mut">{d.minimums[k]}</td><td>{d.rows[rk]}</td><td>{prev ? (prev[rk] ?? 0) : '–'}</td></tr>)}
          <tr><td>{L(l, 'Sesi login kedaluwarsa', 'Expired login sessions')}</td><td className="mut" colSpan={2}>{L(l, 'selalu dibersihkan (7 hari setelah kedaluwarsa)', 'always cleaned (7 days after expiry)')}</td><td>{d.rows.loginSessions}</td><td>{prev ? (prev.loginSessions ?? 0) : '–'}</td></tr></tbody></table>
      <Msg m={msg} />
      {edit && <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}><button className="pri">{L(l, 'Simpan', 'Save')}</button>
        <button type="button" onClick={dry}>{L(l, 'Pratinjau penghapusan', 'Preview deletion')}</button><button type="button" onClick={run}>{L(l, 'Bersihkan sekarang', 'Purge now')}</button></div>}
      <p className="mut" style={{ fontSize: 11 }}>{last ? L(l, 'Pembersihan terakhir: ', 'Last purge: ') + fmt(last.at, me.timezone, l) + (last.ok ? '' : ' (' + L(l, 'gagal', 'failed') + ')') : L(l, 'Belum pernah berjalan.', 'Has not run yet.')}</p>
      <p className="mut" style={{ fontSize: 11 }}>{L(l, 'Catatan: menghapus riwayat sesi backup yang sangat lama dapat mengubah status “basi” menjadi “belum pernah” pada workload yang lama tidak di-backup.',
        'Note: purging very old backup session history can turn a “stale” workload into “never” for workloads that have not been backed up for a long time.')}</p>
    </form></div>
}
