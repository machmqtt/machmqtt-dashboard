import { useState, useEffect, useCallback } from 'react'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'
import { useStore } from '../store/store'
import { TableSkeleton } from '../components/Skeleton'
import { Trash2, Pencil, Plus, X } from 'lucide-react'

interface ServerEntry {
  url: string
}

interface MQTTBridgeEntry {
  name: string
  url: string
  bearer_token: string
}

interface MQTTDiscovery {
  enabled: boolean
  admin_ports: string // comma-separated
}

interface TLSEntry {
  ca_file: string
  insecure: boolean
}

interface ClusterForm {
  name: string
  servers: ServerEntry[]
  mqtt_bridges: MQTTBridgeEntry[]
  mqtt_discovery: MQTTDiscovery | null
  tls: TLSEntry | null
  admin_token: string
}

interface ManagedCluster {
  id: string
  name: string
  servers: ServerEntry[]
  mqtt_bridges: MQTTBridgeEntry[]
  mqtt_discovery: { enabled?: boolean; admin_ports?: number[] } | null
  tls: { ca_file?: string; insecure?: boolean } | null
  admin_token: string
  created_at: string
}

const emptyForm = (): ClusterForm => ({
  name: '',
  servers: [{ url: '' }],
  mqtt_bridges: [],
  mqtt_discovery: null,
  tls: null,
  admin_token: '',
})

function formToRequest(f: ClusterForm) {
  return {
    name: f.name,
    servers: f.servers.filter((s) => s.url.trim()),
    mqtt_bridges: f.mqtt_bridges.filter((b) => b.url.trim()),
    mqtt_discovery: f.mqtt_discovery
      ? {
          enabled: f.mqtt_discovery.enabled,
          admin_ports: f.mqtt_discovery.admin_ports
            .split(',')
            .map((p) => parseInt(p.trim(), 10))
            .filter((p) => !isNaN(p) && p > 0),
        }
      : null,
    tls: f.tls && (f.tls.ca_file.trim() || f.tls.insecure)
      ? { ca_file: f.tls.ca_file.trim() || undefined, insecure: f.tls.insecure }
      : null,
    admin_token: f.admin_token,
  }
}

function clusterToForm(c: ManagedCluster): ClusterForm {
  return {
    name: c.name,
    servers: c.servers.length ? c.servers : [{ url: '' }],
    mqtt_bridges: c.mqtt_bridges || [],
    mqtt_discovery: c.mqtt_discovery
      ? {
          enabled: c.mqtt_discovery.enabled ?? true,
          admin_ports: (c.mqtt_discovery.admin_ports || [8080]).join(', '),
        }
      : null,
    tls: c.tls ? { ca_file: c.tls.ca_file || '', insecure: c.tls.insecure ?? false } : null,
    admin_token: c.admin_token || '',
  }
}

interface ClusterFormEditorProps {
  form: ClusterForm
  onChange: (f: ClusterForm) => void
}

function ClusterFormEditor({ form, onChange }: ClusterFormEditorProps) {
  const set = (patch: Partial<ClusterForm>) => onChange({ ...form, ...patch })

  return (
    <div className="space-y-4">
      {/* Name */}
      <div>
        <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Cluster Name *</label>
        <input
          value={form.name}
          onChange={(e) => set({ name: e.target.value })}
          className="w-full border dark:border-gray-600 dark:bg-gray-700 rounded px-3 py-1.5 text-sm"
          placeholder="production"
        />
      </div>

      {/* Servers */}
      <div>
        <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">NATS Server URLs *</label>
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
                className="flex-1 border dark:border-gray-600 dark:bg-gray-700 rounded px-3 py-1.5 text-sm font-mono"
                placeholder="http://nats-1:8222"
              />
              {form.servers.length > 1 && (
                <button onClick={() => set({ servers: form.servers.filter((_, j) => j !== i) })}
                  className="text-gray-400 hover:text-red-500"><X className="w-4 h-4" /></button>
              )}
            </div>
          ))}
          <button onClick={() => set({ servers: [...form.servers, { url: '' }] })}
            className="text-sm text-brand-blue hover:opacity-80 flex items-center gap-1">
            <Plus className="w-3 h-3" /> Add server
          </button>
        </div>
      </div>

      {/* Admin token */}
      <div>
        <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Admin Token (bearer token for MachMQTT bridge admin API)</label>
        <input
          value={form.admin_token}
          onChange={(e) => set({ admin_token: e.target.value })}
          className="w-full border dark:border-gray-600 dark:bg-gray-700 rounded px-3 py-1.5 text-sm font-mono"
          placeholder="optional"
        />
      </div>

      {/* TLS */}
      <div>
        <div className="flex items-center gap-2 mb-2">
          <label className="text-sm font-medium text-gray-700 dark:text-gray-300">TLS</label>
          <button
            onClick={() => set({ tls: form.tls ? null : { ca_file: '', insecure: false } })}
            className={`text-xs rounded px-2 py-0.5 ${form.tls ? 'bg-green-100 dark:bg-green-900 text-green-700 dark:text-green-300' : 'bg-gray-100 dark:bg-gray-700 text-gray-500'}`}
          >
            {form.tls ? 'Enabled' : 'Disabled'}
          </button>
        </div>
        {form.tls && (
          <div className="space-y-2 pl-2 border-l-2 border-gray-200 dark:border-gray-600">
            <div>
              <label className="block text-xs text-gray-500 dark:text-gray-400 mb-1">CA File path (optional)</label>
              <input
                value={form.tls.ca_file}
                onChange={(e) => set({ tls: { ...form.tls!, ca_file: e.target.value } })}
                className="w-full border dark:border-gray-600 dark:bg-gray-700 rounded px-3 py-1.5 text-sm font-mono"
                placeholder="/path/to/ca.pem"
              />
            </div>
            <label className="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300 cursor-pointer">
              <input type="checkbox" checked={form.tls.insecure}
                onChange={(e) => set({ tls: { ...form.tls!, insecure: e.target.checked } })}
                className="rounded"
              />
              Skip TLS verification (insecure)
            </label>
          </div>
        )}
      </div>

      {/* MQTT Discovery */}
      <div>
        <div className="flex items-center gap-2 mb-2">
          <label className="text-sm font-medium text-gray-700 dark:text-gray-300">MQTT Bridge Discovery</label>
          <button
            onClick={() => set({ mqtt_discovery: form.mqtt_discovery ? null : { enabled: true, admin_ports: '8080' } })}
            className={`text-xs rounded px-2 py-0.5 ${form.mqtt_discovery !== null ? 'bg-green-100 dark:bg-green-900 text-green-700 dark:text-green-300' : 'bg-gray-100 dark:bg-gray-700 text-gray-500'}`}
          >
            {form.mqtt_discovery !== null ? 'Configured' : 'Default (auto-on)'}
          </button>
        </div>
        {form.mqtt_discovery !== null && (
          <div className="space-y-2 pl-2 border-l-2 border-gray-200 dark:border-gray-600">
            <label className="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300 cursor-pointer">
              <input type="checkbox" checked={form.mqtt_discovery.enabled}
                onChange={(e) => set({ mqtt_discovery: { ...form.mqtt_discovery!, enabled: e.target.checked } })}
                className="rounded"
              />
              Enable auto-discovery
            </label>
            <div>
              <label className="block text-xs text-gray-500 dark:text-gray-400 mb-1">Admin ports (comma-separated)</label>
              <input
                value={form.mqtt_discovery.admin_ports}
                onChange={(e) => set({ mqtt_discovery: { ...form.mqtt_discovery!, admin_ports: e.target.value } })}
                className="w-full border dark:border-gray-600 dark:bg-gray-700 rounded px-3 py-1.5 text-sm font-mono"
                placeholder="8080"
              />
            </div>
          </div>
        )}
      </div>

      {/* MQTT Bridges (manual) */}
      <div>
        <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-2">Manual MQTT Bridges</label>
        <div className="space-y-2">
          {form.mqtt_bridges.map((b, i) => (
            <div key={i} className="grid grid-cols-[1fr_1fr_1fr_auto] gap-2 pl-2 border-l-2 border-gray-200 dark:border-gray-600">
              <input value={b.name} onChange={(e) => {
                const mb = [...form.mqtt_bridges]; mb[i] = { ...mb[i], name: e.target.value }; set({ mqtt_bridges: mb })
              }} className="border dark:border-gray-600 dark:bg-gray-700 rounded px-2 py-1.5 text-sm" placeholder="Bridge name" />
              <input value={b.url} onChange={(e) => {
                const mb = [...form.mqtt_bridges]; mb[i] = { ...mb[i], url: e.target.value }; set({ mqtt_bridges: mb })
              }} className="border dark:border-gray-600 dark:bg-gray-700 rounded px-2 py-1.5 text-sm font-mono" placeholder="http://bridge:8080" />
              <input value={b.bearer_token} onChange={(e) => {
                const mb = [...form.mqtt_bridges]; mb[i] = { ...mb[i], bearer_token: e.target.value }; set({ mqtt_bridges: mb })
              }} className="border dark:border-gray-600 dark:bg-gray-700 rounded px-2 py-1.5 text-sm font-mono" placeholder="bearer token (optional)" />
              <button onClick={() => set({ mqtt_bridges: form.mqtt_bridges.filter((_, j) => j !== i) })}
                className="text-gray-400 hover:text-red-500"><X className="w-4 h-4" /></button>
            </div>
          ))}
          <button onClick={() => set({ mqtt_bridges: [...form.mqtt_bridges, { name: '', url: '', bearer_token: '' }] })}
            className="text-sm text-brand-blue hover:opacity-80 flex items-center gap-1">
            <Plus className="w-3 h-3" /> Add bridge
          </button>
        </div>
      </div>
    </div>
  )
}

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
      }
    } catch { /* ignore */ }
    setLoading(false)
  }, [])

  useEffect(() => {
    fetchClusters()
  }, [fetchClusters])

  const handleCreate = async () => {
    if (!createForm.name.trim()) {
      addToast('Cluster name is required', 'error')
      return
    }
    if (!createForm.servers.some((s) => s.url.trim())) {
      addToast('At least one server URL is required', 'error')
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
    } catch { addToast('Network error', 'error') }
    setCreating(false)
  }

  const handleEdit = (c: ManagedCluster) => {
    setEditCluster(c)
    setEditForm(clusterToForm(c))
  }

  const handleSave = async () => {
    if (!editCluster) return
    if (!editForm.name.trim()) { addToast('Cluster name is required', 'error'); return }
    if (!editForm.servers.some((s) => s.url.trim())) { addToast('At least one server URL is required', 'error'); return }
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
    } catch { addToast('Network error', 'error') }
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
    } catch { addToast('Network error', 'error') }
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <div>
          <h1 className="text-2xl font-semibold">Cluster Management</h1>
          <p className="text-sm text-gray-500 dark:text-gray-400 mt-0.5">
            Add and manage NATS clusters. Changes take effect immediately — no restart required.
          </p>
        </div>
        <button
          onClick={() => { setShowCreate(!showCreate); setCreateForm(emptyForm()) }}
          className="bg-brand-blue text-white rounded px-4 py-2 text-sm hover:opacity-90"
        >
          {showCreate ? 'Cancel' : 'Add Cluster'}
        </button>
      </div>

      {showCreate && (
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-5 mb-4">
          <h2 className="text-base font-medium mb-4">New Cluster</h2>
          <ClusterFormEditor form={createForm} onChange={setCreateForm} />
          <div className="mt-4 flex gap-2">
            <button onClick={handleCreate} disabled={creating}
              className="bg-green-600 text-white rounded px-5 py-1.5 text-sm hover:opacity-90 disabled:opacity-50">
              {creating ? 'Creating...' : 'Create Cluster'}
            </button>
            <button onClick={() => setShowCreate(false)}
              className="bg-gray-100 dark:bg-gray-700 text-gray-700 dark:text-gray-200 rounded px-5 py-1.5 text-sm">
              Cancel
            </button>
          </div>
        </div>
      )}

      {loading ? (
        <TableSkeleton rows={2} cols={4} />
      ) : clusters?.length === 0 ? (
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-10 text-center text-gray-400">
          No clusters configured. Click <strong>Add Cluster</strong> to get started.
        </div>
      ) : (
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 dark:bg-gray-700 text-left text-gray-500 dark:text-gray-400">
              <tr>
                <th className="px-4 py-3">Name</th>
                <th className="px-4 py-3">ID</th>
                <th className="px-4 py-3">Servers</th>
                <th className="px-4 py-3">Created</th>
                <th className="px-4 py-3 w-20"></th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
              {clusters?.map((c) => (
                <tr key={c.id} className="hover:bg-gray-50 dark:hover:bg-gray-700/50">
                  <td className="px-4 py-3 font-medium">{c.name}</td>
                  <td className="px-4 py-3 font-mono text-xs text-gray-400">{c.id}</td>
                  <td className="px-4 py-3 text-gray-500 text-xs">
                    {c.servers?.map((s) => s.url).join(', ') || '—'}
                  </td>
                  <td className="px-4 py-3 text-gray-500">
                    {new Date(c.created_at).toLocaleDateString()}
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex gap-2">
                      <button onClick={() => handleEdit(c)}
                        className="text-gray-400 hover:text-brand-blue" title="Edit cluster">
                        <Pencil className="w-4 h-4" />
                      </button>
                      <button onClick={() => handleDelete(c)}
                        className="text-gray-400 hover:text-red-500" title="Delete cluster">
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

      {/* Edit modal */}
      {editCluster && (
        <div className="fixed inset-0 bg-black/30 flex items-start justify-center z-50 overflow-y-auto py-8"
          onClick={() => setEditCluster(null)}>
          <div className="bg-white dark:bg-gray-800 rounded-lg shadow-xl p-6 w-full max-w-2xl mx-4"
            onClick={(e) => e.stopPropagation()}>
            <h2 className="text-lg font-semibold mb-4">Edit Cluster: {editCluster.name}</h2>
            <ClusterFormEditor form={editForm} onChange={setEditForm} />
            <div className="mt-5 flex gap-2">
              <button onClick={handleSave} disabled={saving}
                className="bg-brand-blue text-white rounded px-5 py-1.5 text-sm hover:opacity-90 disabled:opacity-50">
                {saving ? 'Saving...' : 'Save Changes'}
              </button>
              <button onClick={() => setEditCluster(null)}
                className="bg-gray-100 dark:bg-gray-700 text-gray-700 dark:text-gray-200 rounded px-5 py-1.5 text-sm">
                Cancel
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
