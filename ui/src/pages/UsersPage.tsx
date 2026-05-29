import { useState, useEffect, useCallback } from 'react'
import { fetchWithTimeout } from '../utils/fetchWithTimeout'
import { useStore } from '../store/store'
import { TableSkeleton } from '../components/Skeleton'
import { Trash2, Key } from 'lucide-react'

interface ManagedUser {
  id: number
  username: string
  role: string
  created_at: string
  last_login: string | null
  failed_attempts: number
  last_failed_at: string | null
}

export function UsersPage() {
  const addToast = useStore((s) => s.addToast)
  const [users, setUsers] = useState<ManagedUser[] | null>(null)
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [newUsername, setNewUsername] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [newRole, setNewRole] = useState('viewer')
  const [creating, setCreating] = useState(false)
  const [changePwUser, setChangePwUser] = useState<ManagedUser | null>(null)
  const [oldPw, setOldPw] = useState('')
  const [newPw, setNewPw] = useState('')

  const fetchUsers = useCallback(async () => {
    setLoading(true)
    try {
      const res = await fetchWithTimeout('/api/admin/users')
      if (res.ok) {
        const data = await res.json()
        setUsers(data.users || [])
      }
    } catch { /* ignore */ }
    setLoading(false)
  }, [])

  useEffect(() => {
    fetchUsers() // eslint-disable-line react-hooks/set-state-in-effect -- fetch-on-mount is intentional
  }, [fetchUsers])

  const handleCreate = async () => {
    if (!newUsername || !newPassword) {
      addToast('Username and password are required', 'error')
      return
    }
    setCreating(true)
    try {
      const res = await fetchWithTimeout('/api/admin/users', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: newUsername, password: newPassword, role: newRole }),
      })
      if (res.ok) {
        addToast(`User "${newUsername}" created`, 'success')
        setNewUsername(''); setNewPassword(''); setNewRole('viewer'); setShowCreate(false)
        fetchUsers()
      } else {
        const err = await res.json()
        addToast(err.error || 'Failed to create user', 'error')
      }
    } catch { addToast('Network error', 'error') }
    setCreating(false)
  }

  const handleDelete = async (user: ManagedUser) => {
    if (!confirm(`Delete user "${user.username}"?`)) return
    try {
      const res = await fetchWithTimeout(`/api/admin/users/${user.id}`, { method: 'DELETE' })
      if (res.ok) {
        addToast(`User "${user.username}" deleted`, 'success')
        fetchUsers()
      } else {
        const err = await res.json()
        addToast(err.error || 'Failed to delete user', 'error')
      }
    } catch { addToast('Network error', 'error') }
  }

  const handleChangePassword = async () => {
    if (!changePwUser || !oldPw || !newPw) return
    try {
      const res = await fetchWithTimeout(`/api/users/${changePwUser.id}/password`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ old_password: oldPw, new_password: newPw }),
      })
      if (res.ok) {
        addToast('Password changed', 'success')
        setChangePwUser(null); setOldPw(''); setNewPw('')
      } else {
        const err = await res.json()
        addToast(err.error || 'Failed to change password', 'error')
      }
    } catch { addToast('Network error', 'error') }
  }

  const fmtDate = (d: string | null) => {
    if (!d) return 'Never'
    return new Date(d).toLocaleString()
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-2xl font-semibold">User Management</h1>
        <button
          onClick={() => setShowCreate(!showCreate)}
          className="bg-brand-blue text-white rounded px-4 py-2 text-sm hover:opacity-90"
        >
          {showCreate ? 'Cancel' : 'Create User'}
        </button>
      </div>

      {showCreate && (
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow p-4 mb-4">
          <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
            <div>
              <label className="block text-sm text-gray-500 dark:text-gray-400 mb-1">Username</label>
              <input value={newUsername} onChange={(e) => setNewUsername(e.target.value)}
                className="w-full border dark:border-gray-600 dark:bg-gray-700 rounded px-3 py-1.5 text-sm" placeholder="username" />
            </div>
            <div>
              <label className="block text-sm text-gray-500 dark:text-gray-400 mb-1">Password</label>
              <input type="password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)}
                className="w-full border dark:border-gray-600 dark:bg-gray-700 rounded px-3 py-1.5 text-sm" placeholder="password" />
            </div>
            <div>
              <label className="block text-sm text-gray-500 dark:text-gray-400 mb-1">Role</label>
              <select value={newRole} onChange={(e) => setNewRole(e.target.value)}
                className="w-full border dark:border-gray-600 dark:bg-gray-700 rounded px-3 py-1.5 text-sm">
                <option value="viewer">Viewer</option>
                <option value="admin">Admin</option>
              </select>
            </div>
            <div className="flex items-end">
              <button onClick={handleCreate} disabled={creating}
                className="bg-green-600 text-white rounded px-4 py-1.5 text-sm hover:opacity-90 disabled:opacity-50">
                {creating ? 'Creating...' : 'Create'}
              </button>
            </div>
          </div>
        </div>
      )}

      {loading ? (
        <TableSkeleton rows={3} cols={7} />
      ) : (
        <div className="bg-white dark:bg-gray-800 rounded-lg shadow overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 dark:bg-gray-700 text-left text-gray-500 dark:text-gray-400">
              <tr>
                <th className="px-4 py-3">ID</th>
                <th className="px-4 py-3">Username</th>
                <th className="px-4 py-3">Role</th>
                <th className="px-4 py-3">Last Login</th>
                <th className="px-4 py-3">Failed Attempts</th>
                <th className="px-4 py-3">Created</th>
                <th className="px-4 py-3 w-20"></th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
              {users?.map((u) => {
                const isDefaultAdmin = u.id === 1 && u.username === 'admin'
                return (
                  <tr key={u.id} className="hover:bg-gray-50 dark:hover:bg-gray-700/50">
                    <td className="px-4 py-3 font-mono">{u.id}</td>
                    <td className="px-4 py-3 font-medium">{u.username}</td>
                    <td className="px-4 py-3">
                      <span className={`text-xs rounded px-2 py-0.5 ${
                        u.role === 'admin'
                          ? 'bg-purple-100 dark:bg-purple-900 text-purple-700 dark:text-purple-300'
                          : 'bg-gray-100 dark:bg-gray-600 text-gray-700 dark:text-gray-300'
                      }`}>{u.role}</span>
                    </td>
                    <td className="px-4 py-3 text-gray-500">{fmtDate(u.last_login)}</td>
                    <td className="px-4 py-3">
                      {u.failed_attempts > 0 ? (
                        <span className="text-red-500" title={`Last failed: ${fmtDate(u.last_failed_at)}`}>
                          {u.failed_attempts}
                        </span>
                      ) : (
                        <span className="text-gray-400">0</span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-gray-500">{new Date(u.created_at).toLocaleDateString()}</td>
                    <td className="px-4 py-3">
                      <div className="flex gap-2">
                        <button onClick={() => { setChangePwUser(u); setOldPw(''); setNewPw('') }}
                          className="text-gray-400 hover:text-brand-blue" title="Change password">
                          <Key className="w-4 h-4" />
                        </button>
                        {!isDefaultAdmin && (
                          <button onClick={() => handleDelete(u)}
                            className="text-gray-400 hover:text-red-500" title="Delete user">
                            <Trash2 className="w-4 h-4" />
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {changePwUser && (
        <div className="fixed inset-0 bg-black/30 flex items-center justify-center z-50" onClick={() => setChangePwUser(null)}>
          <div className="bg-white dark:bg-gray-800 rounded-lg shadow-xl p-6 w-96" onClick={(e) => e.stopPropagation()}>
            <h2 className="text-lg font-semibold mb-4">Change Password for {changePwUser.username}</h2>
            <div className="space-y-3">
              <div>
                <label className="block text-sm text-gray-500 dark:text-gray-400 mb-1">Current Password</label>
                <input type="password" value={oldPw} onChange={(e) => setOldPw(e.target.value)}
                  className="w-full border dark:border-gray-600 dark:bg-gray-700 rounded px-3 py-1.5 text-sm" />
              </div>
              <div>
                <label className="block text-sm text-gray-500 dark:text-gray-400 mb-1">New Password</label>
                <input type="password" value={newPw} onChange={(e) => setNewPw(e.target.value)}
                  className="w-full border dark:border-gray-600 dark:bg-gray-700 rounded px-3 py-1.5 text-sm" />
              </div>
              <div className="flex gap-2">
                <button onClick={handleChangePassword}
                  className="flex-1 bg-brand-blue text-white rounded py-2 text-sm hover:opacity-90">
                  Change Password
                </button>
                <button onClick={() => setChangePwUser(null)}
                  className="flex-1 bg-gray-100 dark:bg-gray-700 text-gray-700 dark:text-gray-200 rounded py-2 text-sm">
                  Cancel
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
