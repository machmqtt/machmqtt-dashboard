import { useState, useEffect, useCallback } from 'react'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'
import { useStore } from '../store/store'
import { TableSkeleton } from '../components/Skeleton'
import { Trash2, Pencil, Plus, X, Lock, Radio, Cable, Server, ChevronDown } from 'lucide-react'

// ─── Types ──────────────────────────────────────────────────────────────────

interface ServerEntry {
  url: string
}

interface MQTTBridgeEntry {
  name: string
  url: string
  bearer_token: string
  has_bearer_token?: boolean // display-only: a token is stored but not sent back
}

interface TLSForm {
  enabled: boolean
  ca_file: string
  insecure: boolean
}

interface DiscoveryForm {
  enabled: boolean
  admin_ports: string
}

type AuthType = 'none' | 'username_password' | 'token' | 'nkey' | 'creds_file'

interface NATSConnForm {
  enabled: boolean
  urls: string[]
  auth_type: AuthType
  username: string
  password: string
  token: string
  nkey: string
  creds_file: string
  subject_prefix: string
  sys_collection: boolean
}

// secretsSet tracks which secrets already exist server-side (the API returns
// has_* booleans, never the plaintext). Drives "•••• set" placeholders and lets
// the admin leave a field blank to keep the stored value.
interface SecretsSet {
  admin_token: boolean
  password: boolean
  token: boolean
  nkey: boolean
  creds_file: boolean
}

interface ClusterForm {
  name: string
  servers: ServerEntry[]
  mqtt_bridges: MQTTBridgeEntry[]
  discovery: DiscoveryForm
  tls: TLSForm
  admin_token: string
  nats_conn: NATSConnForm
  secrets_set: SecretsSet
}

// ManagedCluster mirrors the redacted clusterView the API returns: secrets are
// replaced by has_* booleans so plaintext never reaches the browser.
interface ManagedCluster {
  id: string
  name: string
  servers: ServerEntry[]
  mqtt_bridges: { name: string; url: string; has_bearer_token?: boolean }[]
  mqtt_discovery: { enabled?: boolean; admin_ports?: number[] } | null
  tls: { ca_file?: string; insecure?: boolean } | null
  has_admin_token?: boolean
  nats_conn: {
    urls?: string[]
    username?: string
    has_password?: boolean
    has_token?: boolean
    has_nkey?: boolean
    has_creds?: boolean
    subject_prefix?: string
    sys_collection?: boolean
  } | null
  created_at: string
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

function parseAdminPorts(s: string): number[] {
  return s.split(',').map((p) => parseInt(p.trim(), 10)).filter((p) => !isNaN(p) && p > 0)
}

const emptyForm = (): ClusterForm => ({
  name: '',
  servers: [{ url: '' }],
  mqtt_bridges: [],
  discovery: { enabled: true, admin_ports: '8080' },
  tls: { enabled: false, ca_file: '', insecure: false },
  admin_token: '',
  nats_conn: {
    enabled: false,
    urls: [''],
    auth_type: 'none',
    username: '',
    password: '',
    token: '',
    nkey: '',
    creds_file: '',
    subject_prefix: '',
    sys_collection: false,
  },
  secrets_set: { admin_token: false, password: false, token: false, nkey: false, creds_file: false },
})

function formToRequest(f: ClusterForm) {
  const ports = parseAdminPorts(f.discovery.admin_ports)
  const natsEnabled = f.nats_conn.enabled && f.nats_conn.urls.some((u) => u.trim())
  return {
    name: f.name,
    servers: f.servers.filter((s) => s.url.trim()),
    mqtt_bridges: f.mqtt_bridges
      .filter((b) => b.url.trim())
      .map((b) => ({ name: b.name, url: b.url, bearer_token: b.bearer_token })),
    mqtt_discovery: {
      enabled: f.discovery.enabled,
      admin_ports: ports.length ? ports : [8080],
    },
    tls: f.tls.enabled
      ? { ca_file: f.tls.ca_file.trim() || undefined, insecure: f.tls.insecure }
      : null,
    admin_token: f.admin_token,
    nats_conn: natsEnabled
      ? {
          urls: f.nats_conn.urls.filter((u) => u.trim()),
          ...(f.nats_conn.auth_type === 'username_password'
            ? { username: f.nats_conn.username, password: f.nats_conn.password }
            : {}),
          ...(f.nats_conn.auth_type === 'token' ? { token: f.nats_conn.token } : {}),
          ...(f.nats_conn.auth_type === 'nkey' ? { nkey: f.nats_conn.nkey } : {}),
          ...(f.nats_conn.auth_type === 'creds_file' ? { creds_file: f.nats_conn.creds_file } : {}),
          subject_prefix: f.nats_conn.subject_prefix.trim() || undefined,
          sys_collection: f.nats_conn.sys_collection || undefined,
        }
      : null,
  }
}

function clusterToForm(c: ManagedCluster): ClusterForm {
  const nc = c.nats_conn
  let auth_type: AuthType = 'none'
  if (nc?.username || nc?.has_password) auth_type = 'username_password'
  else if (nc?.has_token) auth_type = 'token'
  else if (nc?.has_nkey) auth_type = 'nkey'
  else if (nc?.has_creds) auth_type = 'creds_file'
  // Secrets are never returned by the API: leave the inputs blank (a blank value
  // on save means "keep the stored secret") and remember which were set so the
  // editor can show a "•••• set" placeholder.
  return {
    name: c.name,
    servers: c.servers.length ? c.servers : [{ url: '' }],
    mqtt_bridges: (c.mqtt_bridges || []).map((b) => ({
      name: b.name,
      url: b.url,
      bearer_token: '',
      has_bearer_token: b.has_bearer_token,
    })),
    discovery: {
      enabled: c.mqtt_discovery?.enabled !== false,
      admin_ports: (c.mqtt_discovery?.admin_ports || [8080]).join(', '),
    },
    tls: {
      enabled: !!c.tls,
      ca_file: c.tls?.ca_file || '',
      insecure: c.tls?.insecure ?? false,
    },
    admin_token: '',
    nats_conn: {
      enabled: !!nc,
      urls: nc?.urls?.length ? nc.urls : [''],
      auth_type,
      username: nc?.username || '',
      password: '',
      token: '',
      nkey: '',
      creds_file: '',
      subject_prefix: nc?.subject_prefix || '',
      sys_collection: nc?.sys_collection ?? false,
    },
    secrets_set: {
      admin_token: !!c.has_admin_token,
      password: !!nc?.has_password,
      token: !!nc?.has_token,
      nkey: !!nc?.has_nkey,
      creds_file: !!nc?.has_creds,
    },
  }
}

// ─── UI primitives ───────────────────────────────────────────────────────────

function ToggleSwitch({ enabled, onToggle }: { enabled: boolean; onToggle: () => void }) {
  return (
    <button
      type="button"
      onClick={onToggle}
      role="switch"
      aria-checked={enabled}
      className={`relative inline-flex h-5 w-9 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-blue focus-visible:ring-offset-2 ${
        enabled ? 'bg-brand-blue' : 'bg-gray-300 dark:bg-gray-600'
      }`}
    >
      <span
        className={`pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out ${
          enabled ? 'translate-x-4' : 'translate-x-0'
        }`}
      />
    </button>
  )
}

function FieldLabel({ children, hint }: { children: React.ReactNode; hint?: string }) {
  return (
    <div className="mb-1.5">
      <span className="block text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wide">
        {children}
      </span>
      {hint && <span className="block text-xs text-gray-400 dark:text-gray-500 mt-0.5 normal-case font-normal">{hint}</span>}
    </div>
  )
}

const inputCls =
  'w-full rounded-md border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 px-3 py-2 text-sm text-gray-900 dark:text-gray-100 shadow-sm placeholder:text-gray-400 dark:placeholder:text-gray-500 focus:border-brand-blue focus:outline-none focus:ring-1 focus:ring-brand-blue transition-colors'

const monoInputCls = inputCls + ' font-mono'

interface SectionCardProps {
  icon: React.ReactNode
  title: string
  description: string
  enabled: boolean
  onToggle: () => void
  children: React.ReactNode
  defaultExpanded?: boolean
}

function SectionCard({ icon, title, description, enabled, onToggle, children, defaultExpanded = true }: SectionCardProps) {
  const [expanded, setExpanded] = useState(defaultExpanded)

  return (
    <div
      className={`rounded-lg border overflow-hidden transition-colors ${
        enabled
          ? 'border-brand-blue/40 dark:border-brand-blue/50'
          : 'border-gray-200 dark:border-gray-700'
      }`}
    >
      {/* header — click to expand/collapse */}
      <div
        role="button"
        tabIndex={0}
        onClick={() => setExpanded((v) => !v)}
        onKeyDown={(e) => e.key === 'Enter' || e.key === ' ' ? setExpanded((v) => !v) : undefined}
        className="flex items-center justify-between px-4 py-3 cursor-pointer select-none bg-gray-50 dark:bg-gray-800/60"
      >
        <div className="flex items-start gap-3 min-w-0">
          <div className={`mt-0.5 flex-shrink-0 ${enabled ? 'text-brand-blue' : 'text-gray-400 dark:text-gray-500'}`}>
            {icon}
          </div>
          <div className="min-w-0">
            <span className="text-sm font-semibold text-gray-900 dark:text-gray-100">{title}</span>
            <p className="text-xs text-gray-500 dark:text-gray-400 mt-0.5 leading-relaxed">{description}</p>
          </div>
        </div>
        <div className="flex items-center gap-3 flex-shrink-0 ml-4">
          {/* Stop propagation so the toggle doesn't also expand/collapse the card */}
          <div onClick={(e) => e.stopPropagation()}>
            <ToggleSwitch enabled={enabled} onToggle={onToggle} />
          </div>
          <ChevronDown
            className={`w-4 h-4 text-gray-400 transition-transform duration-200 ${expanded ? 'rotate-180' : ''}`}
          />
        </div>
      </div>
      {/* body — shown when expanded, grayed when disabled */}
      {expanded && (
        <div
          className={`px-4 py-4 space-y-4 border-t transition-opacity ${
            enabled
              ? 'border-brand-blue/20 dark:border-brand-blue/30 opacity-100'
              : 'border-gray-100 dark:border-gray-700/50 opacity-40 pointer-events-none select-none'
          }`}
        >
          {children}
        </div>
      )}
    </div>
  )
}

// ─── Form editor ─────────────────────────────────────────────────────────────

interface ClusterFormEditorProps {
  form: ClusterForm
  onChange: (f: ClusterForm) => void
  collapseOptional?: boolean
}

function ClusterFormEditor({ form, onChange, collapseOptional = false }: ClusterFormEditorProps) {
  const set = (patch: Partial<ClusterForm>) => onChange({ ...form, ...patch })

  return (
    <div className="space-y-5">

      {/* ── Cluster name ── */}
      <div>
        <FieldLabel>Cluster Name <span className="text-red-500 normal-case font-normal ml-0.5">*</span></FieldLabel>
        <input
          value={form.name}
          onChange={(e) => set({ name: e.target.value })}
          className={inputCls}
          placeholder="production"
        />
      </div>

      {/* ── NATS Monitoring URLs ── */}
      <div>
        <FieldLabel hint="HTTP monitoring endpoints for metrics polling — e.g. http://nats-1:8222">
          NATS Monitoring URLs <span className="text-red-500 normal-case font-normal ml-0.5">*</span>
        </FieldLabel>
        <div className="space-y-2">
          {form.servers.map((srv, i) => (
            <div key={i} className="flex gap-2">
              <input
                value={srv.url}
                onChange={(e) => {
                  const servers = [...form.servers]
                  servers[i] = { url: e.target.value }
                  set({ servers })
                }}
                className={monoInputCls}
                placeholder="http://nats-1:8222"
              />
              {form.servers.length > 1 && (
                <button
                  type="button"
                  onClick={() => set({ servers: form.servers.filter((_, j) => j !== i) })}
                  className="text-gray-400 hover:text-red-500 transition-colors"
                >
                  <X className="w-4 h-4" />
                </button>
              )}
            </div>
          ))}
          <button
            type="button"
            onClick={() => set({ servers: [...form.servers, { url: '' }] })}
            className="flex items-center gap-1.5 text-xs text-brand-blue hover:opacity-80 transition-opacity"
          >
            <Plus className="w-3.5 h-3.5" /> Add server
          </button>
        </div>
      </div>

      {/* ── Admin token ── */}
      <div>
        <FieldLabel hint="Bearer token for the MachMQTT bridge admin API. Leave blank if the admin API has no auth.">
          Admin Token
        </FieldLabel>
        <input
          value={form.admin_token}
          onChange={(e) => set({ admin_token: e.target.value })}
          className={monoInputCls}
          placeholder={form.secrets_set.admin_token ? '•••• set — leave blank to keep' : 'optional'}
          type="password"
          autoComplete="new-password"
        />
      </div>

      {/* ── TLS ── */}
      <SectionCard
        defaultExpanded={!collapseOptional}
        icon={<Lock className="w-4 h-4" />}
        title="TLS"
        description="Configure TLS for connections to NATS monitoring endpoints. Required when your NATS servers use HTTPS."
        enabled={form.tls.enabled}
        onToggle={() =>
          set({ tls: { ...form.tls, enabled: !form.tls.enabled } })
        }
      >
        <div>
          <FieldLabel hint="Path to the CA certificate file on the dashboard server's filesystem.">CA File Path</FieldLabel>
          <input
            value={form.tls.ca_file}
            onChange={(e) => set({ tls: { ...form.tls, ca_file: e.target.value } })}
            className={monoInputCls}
            placeholder="/etc/ssl/certs/ca.pem"
          />
        </div>
        <label className="flex items-center gap-2.5 cursor-pointer group">
          <input
            type="checkbox"
            checked={form.tls.insecure}
            onChange={(e) => set({ tls: { ...form.tls, insecure: e.target.checked } })}
            className="rounded border-gray-300 dark:border-gray-600 text-brand-blue focus:ring-brand-blue"
          />
          <span className="text-sm text-gray-700 dark:text-gray-300">
            Skip TLS verification{' '}
            <span className="text-xs text-amber-600 dark:text-amber-400">(insecure — use only in development)</span>
          </span>
        </label>
      </SectionCard>

      {/* ── MachMQTT Discovery ── */}
      <SectionCard
        defaultExpanded={!collapseOptional}
        icon={<Radio className="w-4 h-4" />}
        title="MachMQTT Discovery"
        description="Automatically discovers MachMQTT bridge instances by scanning active NATS connections and probing their admin HTTP endpoints. The dashboard reads each server's connection list, identifies bridge pool connections by name, and probes their admin port to fetch real-time status and metrics. Discovery runs on every slow poll cycle."
        enabled={form.discovery.enabled}
        onToggle={() =>
          set({ discovery: { ...form.discovery, enabled: !form.discovery.enabled } })
        }
      >
        <div>
          <FieldLabel hint="Comma-separated list of admin HTTP ports to probe. MachMQTT default is 8080.">
            Admin Ports
          </FieldLabel>
          <input
            value={form.discovery.admin_ports}
            onChange={(e) =>
              set({ discovery: { ...form.discovery, admin_ports: e.target.value } })
            }
            className={monoInputCls}
            placeholder="8080"
          />
        </div>
      </SectionCard>

      {/* ── NATS Push Connection ── */}
      <SectionCard
        defaultExpanded={!collapseOptional}
        icon={<Cable className="w-4 h-4" />}
        title="NATS Push Collection"
        description="Connect directly to NATS using the native nats:// protocol to receive real-time bridge metrics and server stats via push subscription. Enables $SYS-based server collection and eliminates polling overhead. Uses nats:// seed URLs (port 4222 by default)."
        enabled={form.nats_conn.enabled}
        onToggle={() =>
          set({ nats_conn: { ...form.nats_conn, enabled: !form.nats_conn.enabled } })
        }
      >
        {/* NATS seed URLs */}
        <div>
          <FieldLabel hint="NATS client connection URLs — nats:// protocol, port 4222 by default.">
            NATS Seed URLs
          </FieldLabel>
          <div className="space-y-2">
            {form.nats_conn.urls.map((url, i) => (
              <div key={i} className="flex gap-2">
                <input
                  value={url}
                  onChange={(e) => {
                    const urls = [...form.nats_conn.urls]
                    urls[i] = e.target.value
                    set({ nats_conn: { ...form.nats_conn, urls } })
                  }}
                  className={monoInputCls}
                  placeholder="nats://nats-1:4222"
                />
                {form.nats_conn.urls.length > 1 && (
                  <button
                    type="button"
                    onClick={() =>
                      set({
                        nats_conn: {
                          ...form.nats_conn,
                          urls: form.nats_conn.urls.filter((_, j) => j !== i),
                        },
                      })
                    }
                    className="text-gray-400 hover:text-red-500 transition-colors"
                  >
                    <X className="w-4 h-4" />
                  </button>
                )}
              </div>
            ))}
            <button
              type="button"
              onClick={() =>
                set({ nats_conn: { ...form.nats_conn, urls: [...form.nats_conn.urls, ''] } })
              }
              className="flex items-center gap-1.5 text-xs text-brand-blue hover:opacity-80 transition-opacity"
            >
              <Plus className="w-3.5 h-3.5" /> Add URL
            </button>
          </div>
        </div>

        {/* Auth type */}
        <div>
          <FieldLabel>Authentication</FieldLabel>
          <select
            value={form.nats_conn.auth_type}
            onChange={(e) =>
              set({ nats_conn: { ...form.nats_conn, auth_type: e.target.value as AuthType } })
            }
            className={inputCls}
          >
            <option value="none">None</option>
            <option value="username_password">Username / Password</option>
            <option value="token">Token</option>
            <option value="nkey">NKey</option>
            <option value="creds_file">Credentials File (.creds)</option>
          </select>
        </div>

        {form.nats_conn.auth_type === 'username_password' && (
          <div className="grid grid-cols-2 gap-3">
            <div>
              <FieldLabel>Username</FieldLabel>
              <input
                value={form.nats_conn.username}
                onChange={(e) => set({ nats_conn: { ...form.nats_conn, username: e.target.value } })}
                className={inputCls}
                placeholder="user"
                autoComplete="off"
              />
            </div>
            <div>
              <FieldLabel>Password</FieldLabel>
              <input
                value={form.nats_conn.password}
                onChange={(e) => set({ nats_conn: { ...form.nats_conn, password: e.target.value } })}
                className={monoInputCls}
                placeholder={form.secrets_set.password ? '•••• set — leave blank to keep' : '••••••••'}
                type="password"
                autoComplete="new-password"
              />
            </div>
          </div>
        )}

        {form.nats_conn.auth_type === 'token' && (
          <div>
            <FieldLabel>Token</FieldLabel>
            <input
              value={form.nats_conn.token}
              onChange={(e) => set({ nats_conn: { ...form.nats_conn, token: e.target.value } })}
              className={monoInputCls}
              placeholder={form.secrets_set.token ? '•••• set — leave blank to keep' : 'secret-token'}
              type="password"
              autoComplete="new-password"
            />
          </div>
        )}

        {form.nats_conn.auth_type === 'nkey' && (
          <div>
            <FieldLabel hint="NKey seed string starting with S.">NKey Seed</FieldLabel>
            <input
              value={form.nats_conn.nkey}
              onChange={(e) => set({ nats_conn: { ...form.nats_conn, nkey: e.target.value } })}
              className={monoInputCls}
              placeholder={form.secrets_set.nkey ? '•••• set — leave blank to keep' : 'SUAM…'}
            />
          </div>
        )}

        {form.nats_conn.auth_type === 'creds_file' && (
          <div>
            <FieldLabel hint="Path to the .creds file on the dashboard server's filesystem.">
              Credentials File Path
            </FieldLabel>
            <input
              value={form.nats_conn.creds_file}
              onChange={(e) => set({ nats_conn: { ...form.nats_conn, creds_file: e.target.value } })}
              className={monoInputCls}
              placeholder={form.secrets_set.creds_file ? '•••• set — leave blank to keep' : '/etc/nats/user.creds'}
            />
          </div>
        )}

        {/* Subject prefix */}
        <div>
          <FieldLabel hint="Must match the subject_prefix configured in MachMQTT for this cluster. Default is $MQTT5.">
            Subject Prefix
          </FieldLabel>
          <input
            value={form.nats_conn.subject_prefix}
            onChange={(e) => set({ nats_conn: { ...form.nats_conn, subject_prefix: e.target.value } })}
            className={monoInputCls}
            placeholder="$MQTT5"
          />
        </div>

        {/* $SYS collection */}
        <label className="flex items-center gap-2.5 cursor-pointer">
          <input
            type="checkbox"
            checked={form.nats_conn.sys_collection}
            onChange={(e) =>
              set({ nats_conn: { ...form.nats_conn, sys_collection: e.target.checked } })
            }
            className="rounded border-gray-300 dark:border-gray-600 text-brand-blue focus:ring-brand-blue"
          />
          <div>
            <span className="text-sm font-medium text-gray-700 dark:text-gray-300">
              Enable $SYS collection
            </span>
            <p className="text-xs text-gray-500 dark:text-gray-400 mt-0.5">
              Requires system-account credentials. Replaces HTTP polling for server stats with push-based $SYS.SERVER.*.STATSZ subscriptions.
            </p>
          </div>
        </label>
      </SectionCard>

      {/* ── Manual MQTT Bridges ── */}
      <div>
        <div className="flex items-center gap-2 mb-3">
          <Server className="w-4 h-4 text-gray-400" />
          <span className="text-sm font-semibold text-gray-700 dark:text-gray-300">Manual Bridges</span>
          <span className="text-xs text-gray-400 dark:text-gray-500">Optional — add bridges that aren't auto-discovered</span>
        </div>
        <div className="space-y-2">
          {form.mqtt_bridges.length > 0 && (
            <div className="grid grid-cols-[1fr_1fr_1fr_auto] gap-2 mb-1">
              <span className="text-[10px] font-semibold text-gray-400 uppercase tracking-wide px-1">Name</span>
              <span className="text-[10px] font-semibold text-gray-400 uppercase tracking-wide px-1">Admin URL</span>
              <span className="text-[10px] font-semibold text-gray-400 uppercase tracking-wide px-1">Bearer Token</span>
              <span />
            </div>
          )}
          {form.mqtt_bridges.map((b, i) => (
            <div key={i} className="grid grid-cols-[1fr_1fr_1fr_auto] gap-2">
              <input
                value={b.name}
                onChange={(e) => {
                  const mb = [...form.mqtt_bridges]
                  mb[i] = { ...mb[i], name: e.target.value }
                  set({ mqtt_bridges: mb })
                }}
                className={inputCls}
                placeholder="my-bridge"
              />
              <input
                value={b.url}
                onChange={(e) => {
                  const mb = [...form.mqtt_bridges]
                  mb[i] = { ...mb[i], url: e.target.value }
                  set({ mqtt_bridges: mb })
                }}
                className={monoInputCls}
                placeholder="http://bridge:8080"
              />
              <input
                value={b.bearer_token}
                onChange={(e) => {
                  const mb = [...form.mqtt_bridges]
                  mb[i] = { ...mb[i], bearer_token: e.target.value }
                  set({ mqtt_bridges: mb })
                }}
                className={monoInputCls}
                placeholder={b.has_bearer_token ? '•••• set — leave blank to keep' : 'token (optional)'}
              />
              <button
                type="button"
                onClick={() => set({ mqtt_bridges: form.mqtt_bridges.filter((_, j) => j !== i) })}
                className="text-gray-400 hover:text-red-500 transition-colors"
              >
                <X className="w-4 h-4" />
              </button>
            </div>
          ))}
          <button
            type="button"
            onClick={() =>
              set({ mqtt_bridges: [...form.mqtt_bridges, { name: '', url: '', bearer_token: '' }] })
            }
            className="flex items-center gap-1.5 text-xs text-brand-blue hover:opacity-80 transition-opacity"
          >
            <Plus className="w-3.5 h-3.5" /> Add bridge
          </button>
        </div>
      </div>
    </div>
  )
}

// ─── Page ────────────────────────────────────────────────────────────────────

interface ClustersPageProps {
  onClustersChanged: () => void
}

export function ClustersPage({ onClustersChanged }: ClustersPageProps) {
  const addToast = useStore((s) => s.addToast)
  const [clusters, setClusters] = useState<ManagedCluster[] | null>(null)
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [createForm, setCreateForm] = useState<ClusterForm>(emptyForm())
  const [creating, setCreating] = useState(false)
  const [editCluster, setEditCluster] = useState<ManagedCluster | null>(null)
  const [editForm, setEditForm] = useState<ClusterForm>(emptyForm())
  const [saving, setSaving] = useState(false)

  const fetchClusters = useCallback(async () => {
    setLoading(true)
    try {
      const res = await fetchWithTimeout('/api/admin/clusters')
      if (res.ok) {
        const data = await res.json()
        setClusters(data.clusters || [])
      } else {
        addToast('Failed to load clusters', 'error')
      }
    } catch {
      addToast('Network error loading clusters', 'error')
    }
    setLoading(false)
  }, [addToast])

  useEffect(() => {
    fetchClusters() // eslint-disable-line react-hooks/set-state-in-effect -- fetch-on-mount is intentional
  }, [fetchClusters])

  const handleCreate = async () => {
    if (!createForm.name.trim()) {
      addToast('Cluster name is required', 'error')
      return
    }
    if (!createForm.servers.some((s) => s.url.trim())) {
      addToast('At least one monitoring URL is required', 'error')
      return
    }
    setCreating(true)
    try {
      const res = await fetchWithTimeout('/api/admin/clusters', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(formToRequest(createForm)),
      })
      if (res.ok) {
        addToast(`Cluster "${createForm.name}" created`, 'success')
        setShowCreate(false)
        setCreateForm(emptyForm())
        fetchClusters()
        onClustersChanged()
      } else {
        const err = await res.json().catch(() => ({ error: 'Failed to create cluster' }))
        addToast(err.error || 'Failed to create cluster', 'error')
      }
    } catch {
      addToast('Network error', 'error')
    }
    setCreating(false)
  }

  const handleEdit = (c: ManagedCluster) => {
    setEditCluster(c)
    setEditForm(clusterToForm(c))
  }

  const handleSave = async () => {
    if (!editCluster) return
    if (!editForm.name.trim()) { addToast('Cluster name is required', 'error'); return }
    if (!editForm.servers.some((s) => s.url.trim())) { addToast('At least one monitoring URL is required', 'error'); return }
    setSaving(true)
    try {
      const res = await fetchWithTimeout(`/api/admin/clusters/${editCluster.id}`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(formToRequest(editForm)),
      })
      if (res.ok) {
        addToast(`Cluster "${editForm.name}" updated`, 'success')
        setEditCluster(null)
        fetchClusters()
        onClustersChanged()
      } else {
        const err = await res.json().catch(() => ({ error: 'Failed to update cluster' }))
        addToast(err.error || 'Failed to update cluster', 'error')
      }
    } catch {
      addToast('Network error', 'error')
    }
    setSaving(false)
  }

  const handleDelete = async (c: ManagedCluster) => {
    if (!confirm(`Delete cluster "${c.name}"? This will remove all associated metrics and topology data.`)) return
    try {
      const res = await fetchWithTimeout(`/api/admin/clusters/${c.id}`, { method: 'DELETE' })
      if (res.ok) {
        addToast(`Cluster "${c.name}" deleted`, 'success')
        fetchClusters()
        onClustersChanged()
      } else {
        const err = await res.json().catch(() => ({ error: 'Failed to delete cluster' }))
        addToast(err.error || 'Failed to delete cluster', 'error')
      }
    } catch {
      addToast('Network error', 'error')
    }
  }

  return (
    <div>
      {/* ── Page header ── */}
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-semibold">Cluster Management</h1>
          <p className="text-sm text-gray-500 dark:text-gray-400 mt-0.5">
            Add and manage NATS clusters. Changes take effect immediately — no restart required.
          </p>
        </div>
        <button
          onClick={() => {
            setShowCreate(!showCreate)
            setCreateForm(emptyForm())
          }}
          className="inline-flex items-center gap-2 bg-brand-blue text-white rounded-lg px-4 py-2 text-sm font-medium hover:opacity-90 transition-opacity shadow-sm"
        >
          <Plus className="w-4 h-4" />
          {showCreate ? 'Cancel' : 'Add Cluster'}
        </button>
      </div>

      {/* ── Create form ── */}
      {showCreate && (
        <div className="bg-white dark:bg-gray-800 rounded-xl shadow-md border border-gray-200 dark:border-gray-700 p-6 mb-6">
          <h2 className="text-base font-semibold text-gray-900 dark:text-gray-100 mb-5">New Cluster</h2>
          <ClusterFormEditor form={createForm} onChange={setCreateForm} />
          <div className="mt-6 flex gap-3 pt-5 border-t border-gray-100 dark:border-gray-700">
            <button
              onClick={handleCreate}
              disabled={creating}
              className="inline-flex items-center gap-2 bg-green-600 hover:bg-green-700 text-white rounded-lg px-5 py-2 text-sm font-medium transition-colors disabled:opacity-50"
            >
              {creating ? 'Creating…' : 'Create Cluster'}
            </button>
            <button
              onClick={() => setShowCreate(false)}
              className="inline-flex items-center gap-2 bg-gray-100 dark:bg-gray-700 hover:bg-gray-200 dark:hover:bg-gray-600 text-gray-700 dark:text-gray-200 rounded-lg px-5 py-2 text-sm font-medium transition-colors"
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {/* ── Cluster list ── */}
      {loading ? (
        <TableSkeleton rows={2} cols={4} />
      ) : clusters?.length === 0 ? (
        <div className="bg-white dark:bg-gray-800 rounded-xl shadow border border-gray-200 dark:border-gray-700 p-12 text-center">
          <Server className="w-10 h-10 text-gray-300 dark:text-gray-600 mx-auto mb-3" />
          <p className="text-gray-500 dark:text-gray-400 text-sm">
            No clusters configured yet. Click <strong>Add Cluster</strong> to get started.
          </p>
        </div>
      ) : (
        <div className="bg-white dark:bg-gray-800 rounded-xl shadow border border-gray-200 dark:border-gray-700 overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 dark:bg-gray-700/50 text-left">
              <tr>
                <th className="px-4 py-3 text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wide">Name</th>
                <th className="px-4 py-3 text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wide">ID</th>
                <th className="px-4 py-3 text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wide">Monitoring Servers</th>
                <th className="px-4 py-3 text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wide">Features</th>
                <th className="px-4 py-3 text-xs font-semibold text-gray-500 dark:text-gray-400 uppercase tracking-wide">Created</th>
                <th className="px-4 py-3 w-20" />
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
              {clusters?.map((c) => (
                <tr key={c.id} className="hover:bg-gray-50 dark:hover:bg-gray-700/30 transition-colors">
                  <td className="px-4 py-3 font-semibold text-gray-900 dark:text-gray-100">{c.name}</td>
                  <td className="px-4 py-3 font-mono text-xs text-gray-400">{c.id}</td>
                  <td className="px-4 py-3 text-gray-500 dark:text-gray-400 text-xs">
                    {c.servers?.map((s) => s.url).join(', ') || '—'}
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex gap-1.5 flex-wrap">
                      {c.tls && (
                        <span className="inline-flex items-center gap-1 text-[10px] font-medium bg-blue-50 dark:bg-blue-900/30 text-blue-700 dark:text-blue-300 rounded px-1.5 py-0.5">
                          <Lock className="w-2.5 h-2.5" /> TLS
                        </span>
                      )}
                      {c.mqtt_discovery?.enabled !== false && (
                        <span className="inline-flex items-center gap-1 text-[10px] font-medium bg-purple-50 dark:bg-purple-900/30 text-purple-700 dark:text-purple-300 rounded px-1.5 py-0.5">
                          <Radio className="w-2.5 h-2.5" /> Discovery
                        </span>
                      )}
                      {c.nats_conn && (
                        <span className="inline-flex items-center gap-1 text-[10px] font-medium bg-green-50 dark:bg-green-900/30 text-green-700 dark:text-green-300 rounded px-1.5 py-0.5">
                          <Cable className="w-2.5 h-2.5" /> NATS Push
                        </span>
                      )}
                    </div>
                  </td>
                  <td className="px-4 py-3 text-gray-500 dark:text-gray-400 text-sm">
                    {new Date(c.created_at).toLocaleDateString()}
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex gap-2">
                      <button
                        onClick={() => handleEdit(c)}
                        className="text-gray-400 hover:text-brand-blue transition-colors"
                        title="Edit cluster"
                      >
                        <Pencil className="w-4 h-4" />
                      </button>
                      <button
                        onClick={() => handleDelete(c)}
                        className="text-gray-400 hover:text-red-500 transition-colors"
                        title="Delete cluster"
                      >
                        <Trash2 className="w-4 h-4" />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* ── Edit modal ── */}
      {editCluster && (
        <div
          className="fixed inset-0 bg-black/40 backdrop-blur-sm flex items-start justify-center z-50 overflow-y-auto py-8"
          onClick={() => setEditCluster(null)}
        >
          <div
            className="bg-white dark:bg-gray-800 rounded-xl shadow-2xl border border-gray-200 dark:border-gray-700 p-6 w-full max-w-2xl mx-4 my-auto"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-center justify-between mb-5">
              <h2 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
                Edit Cluster: <span className="text-brand-blue">{editCluster.name}</span>
              </h2>
              <button
                onClick={() => setEditCluster(null)}
                className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 transition-colors"
              >
                <X className="w-5 h-5" />
              </button>
            </div>
            <ClusterFormEditor form={editForm} onChange={setEditForm} collapseOptional />
            <div className="mt-6 flex gap-3 pt-5 border-t border-gray-100 dark:border-gray-700">
              <button
                onClick={handleSave}
                disabled={saving}
                className="inline-flex items-center gap-2 bg-brand-blue hover:opacity-90 text-white rounded-lg px-5 py-2 text-sm font-medium transition-opacity disabled:opacity-50"
              >
                {saving ? 'Saving…' : 'Save Changes'}
              </button>
              <button
                onClick={() => setEditCluster(null)}
                className="inline-flex items-center gap-2 bg-gray-100 dark:bg-gray-700 hover:bg-gray-200 dark:hover:bg-gray-600 text-gray-700 dark:text-gray-200 rounded-lg px-5 py-2 text-sm font-medium transition-colors"
              >
                Cancel
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
