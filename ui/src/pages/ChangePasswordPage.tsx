import { useState, type FormEvent } from 'react'
import { Server } from 'lucide-react'

interface Props {
  userId: number
  onChanged: () => void
}

export function ChangePasswordPage({ userId, onChanged }: Props) {
  const [oldPassword, setOldPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setError('')

    if (newPassword.length < 8) {
      setError('New password must be at least 8 characters')
      return
    }
    if (newPassword !== confirmPassword) {
      setError('Passwords do not match')
      return
    }

    setLoading(true)
    try {
      const res = await fetch(`/api/users/${userId}/password`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ old_password: oldPassword, new_password: newPassword }),
      })
      if (!res.ok) {
        const data = await res.json().catch(() => ({ error: 'Failed to change password' }))
        throw new Error(data.error || 'Failed to change password')
      }
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to change password')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen bg-brand-dark flex items-center justify-center">
      <div className="bg-white rounded-lg shadow-xl p-8 w-96">
        <div className="flex items-center justify-center gap-2 mb-4">
          <Server className="w-8 h-8 text-brand-blue" />
          <h1 className="text-2xl font-semibold">MachMQTT Dashboard</h1>
        </div>
        <p className="text-sm text-gray-600 text-center mb-6">
          You must change your password before continuing.
        </p>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label htmlFor="current-password" className="block text-sm font-medium text-gray-700 mb-1">Current Password</label>
            <input
              id="current-password"
              type="password"
              autoComplete="current-password"
              value={oldPassword}
              onChange={(e) => setOldPassword(e.target.value)}
              className="w-full border rounded px-3 py-2 outline-none focus:ring-2 focus:ring-brand-blue"
              autoFocus
            />
          </div>
          <div>
            <label htmlFor="new-password" className="block text-sm font-medium text-gray-700 mb-1">New Password</label>
            <input
              id="new-password"
              type="password"
              autoComplete="new-password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              className="w-full border rounded px-3 py-2 outline-none focus:ring-2 focus:ring-brand-blue"
            />
          </div>
          <div>
            <label htmlFor="confirm-password" className="block text-sm font-medium text-gray-700 mb-1">Confirm New Password</label>
            <input
              id="confirm-password"
              type="password"
              autoComplete="new-password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              className="w-full border rounded px-3 py-2 outline-none focus:ring-2 focus:ring-brand-blue"
            />
          </div>
          {error && <p className="text-red-500 text-sm">{error}</p>}
          <button
            type="submit"
            disabled={loading}
            className="w-full bg-brand-blue text-white rounded py-2 font-medium hover:opacity-90 disabled:opacity-50"
          >
            {loading ? 'Changing...' : 'Change Password'}
          </button>
        </form>
      </div>
    </div>
  )
}
