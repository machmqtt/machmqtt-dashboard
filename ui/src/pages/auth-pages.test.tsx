import { fireEvent, render, screen, waitFor, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { describe, expect, it, vi } from 'vitest'
import { ChangePasswordPage } from './ChangePasswordPage'
import { LoginPage } from './LoginPage'

function renderLogin(props: React.ComponentProps<typeof LoginPage>) {
  return render(<MemoryRouter><LoginPage {...props} /></MemoryRouter>)
}

async function fillPasswordForm(current: string, next: string, confirm: string) {
  const user = userEvent.setup()
  await user.type(screen.getByLabelText('Current Password'), current)
  await user.type(screen.getByLabelText('New Password'), next)
  await user.type(screen.getByLabelText('Confirm New Password'), confirm)
  await user.click(screen.getByRole('button', { name: 'Change Password' }))
}

describe('LoginPage', () => {
  it('submits password credentials and exposes the local break-glass route', async () => {
    const onLogin = vi.fn().mockResolvedValue(undefined)
    renderLogin({ onLogin, providers: [{ name: 'Corporate LDAP', type: 'ldap' }] })
    const user = userEvent.setup()
    await user.type(screen.getByLabelText('Username'), 'alice')
    await user.type(screen.getByLabelText('Password'), 'secret')
    await user.click(screen.getByRole('button', { name: 'Sign In' }))
    await waitFor(() => expect(onLogin).toHaveBeenCalledWith('alice', 'secret'))
    expect(screen.getByRole('link', { name: 'Local administrator login' })).toHaveAttribute('href', '/login/local')
    // No OIDC provider means no SSO section, so the "or" separator would be
    // dangling above an empty area.
    expect(screen.queryByText('or')).not.toBeInTheDocument()
  })

  // Double-submitting a login is a credential-stuffing amplifier and races two
  // sessions, so the button must lock for the duration of the request.
  it('locks the submit button while the credential check is in flight', async () => {
    let release!: () => void
    const onLogin = vi.fn(() => new Promise<void>((resolve) => { release = resolve }))
    renderLogin({ onLogin })
    const user = userEvent.setup()
    await user.type(screen.getByLabelText('Username'), 'alice')
    await user.type(screen.getByLabelText('Password'), 'secret')
    await user.click(screen.getByRole('button', { name: 'Sign In' }))

    const pending = await screen.findByRole('button', { name: 'Signing in...' })
    expect(pending).toBeDisabled()
    await user.click(pending)
    expect(onLogin).toHaveBeenCalledTimes(1)

    await act(async () => { release() })
    await waitFor(() => expect(screen.getByRole('button', { name: 'Sign In' })).toBeEnabled())
  })

  it('reports rejected credentials and restores the submit button', async () => {
    renderLogin({ onLogin: vi.fn().mockRejectedValue(new Error('denied')) })
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Sign In' }))
    expect(await screen.findByText('Invalid credentials')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Sign In' })).toBeEnabled()
  })

  it('renders OIDC-only, combined, and local-only variants safely', async () => {
    const onOIDCLogin = vi.fn()
    const oidcOnly = renderLogin({
      onLogin: vi.fn(),
      onOIDCLogin,
      providers: [{ name: 'SSO', type: 'oidc', login_url: '/api/auth/oidc/SSO/login' }],
    })
    expect(screen.getByRole('button', { name: 'Sign in with SSO' })).toBeInTheDocument()
    expect(screen.queryByLabelText('Username')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Sign in with SSO' }))
    expect(onOIDCLogin).toHaveBeenCalledOnce()
    expect(onOIDCLogin).toHaveBeenCalledWith('/api/auth/oidc/SSO/login')
    expect(screen.queryByText('or')).not.toBeInTheDocument()
    oidcOnly.unmount()

    const combined = renderLogin({ onLogin: vi.fn(), onOIDCLogin, providers: [
      { name: 'SSO', type: 'oidc', login_url: '/api/auth/oidc/SSO/login' },
      { name: 'Directory', type: 'ldap' },
    ] })
    expect(screen.getByText('or')).toBeInTheDocument()
    expect(screen.getByLabelText('Username')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Sign in with Directory' })).not.toBeInTheDocument()
    combined.unmount()

    renderLogin({ onLogin: vi.fn(), onOIDCLogin, localOnly: true, providers: [
      { name: 'SSO', type: 'oidc', login_url: '/ignored' },
    ] })
    expect(screen.getByText('Local administrator sign in')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Sign in with/ })).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Return to organization sign in' })).toHaveAttribute('href', '/login')
    expect(onOIDCLogin).toHaveBeenCalledTimes(1)
  })

  it('does not navigate when an OIDC provider lacks a login URL', () => {
    const onOIDCLogin = vi.fn()
    renderLogin({ onLogin: vi.fn(), onOIDCLogin, providers: [{ name: 'Incomplete', type: 'oidc' }] })
    fireEvent.click(screen.getByRole('button', { name: 'Sign in with Incomplete' }))
    expect(onOIDCLogin).not.toHaveBeenCalled()
  })

  it('uses the browser navigation adapter by default', () => {
    renderLogin({
      onLogin: vi.fn(),
      providers: [{ name: 'Hash SSO', type: 'oidc', login_url: '#oidc-start' }],
    })
    fireEvent.click(screen.getByRole('button', { name: 'Sign in with Hash SSO' }))
    expect(window.location.hash).toBe('#oidc-start')
    window.history.replaceState({}, '', '/')
  })
})

describe('ChangePasswordPage', () => {
  it('validates password length and equality before sending a request', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch')
    const onChanged = vi.fn()
    const { rerender } = render(<ChangePasswordPage userId={7} onChanged={onChanged} />)
    await fillPasswordForm('old', 'short', 'short')
    expect(screen.getByText('New password must be at least 8 characters')).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()

    rerender(<ChangePasswordPage userId={8} onChanged={onChanged} />)
    const inputs = screen.getAllByLabelText(/Password/)
    fireEvent.change(inputs[1], { target: { value: 'long-enough' } })
    fireEvent.change(inputs[2], { target: { value: 'different' } })
    fireEvent.click(screen.getByRole('button', { name: 'Change Password' }))
    expect(await screen.findByText('Passwords do not match')).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('changes a password and reports server, malformed, and network errors', async () => {
    const onChanged = vi.fn()
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response(null, { status: 204 }))
    const { unmount } = render(<ChangePasswordPage userId={12} onChanged={onChanged} />)
    await fillPasswordForm('old-pass', 'new-password', 'new-password')
    await waitFor(() => expect(onChanged).toHaveBeenCalled())
    expect(fetch).toHaveBeenCalledWith('/api/users/12/password', expect.objectContaining({
      method: 'PUT',
      body: JSON.stringify({ old_password: 'old-pass', new_password: 'new-password' }),
    }))
    unmount()

    vi.mocked(fetch).mockResolvedValueOnce(new Response(JSON.stringify({ error: 'wrong old password' }), {
      status: 400,
      headers: { 'Content-Type': 'application/json' },
    }))
    const second = render(<ChangePasswordPage userId={12} onChanged={onChanged} />)
    await fillPasswordForm('old-pass', 'new-password', 'new-password')
    expect(await screen.findByText('wrong old password')).toBeInTheDocument()
    second.unmount()

    vi.mocked(fetch).mockResolvedValueOnce(new Response('not-json', { status: 500 }))
    const third = render(<ChangePasswordPage userId={12} onChanged={onChanged} />)
    await fillPasswordForm('old-pass', 'new-password', 'new-password')
    expect(await screen.findByText('Failed to change password')).toBeInTheDocument()
    third.unmount()

    vi.mocked(fetch).mockResolvedValueOnce(new Response(JSON.stringify({}), {
      status: 400,
      headers: { 'Content-Type': 'application/json' },
    }))
    const fourth = render(<ChangePasswordPage userId={12} onChanged={onChanged} />)
    await fillPasswordForm('old-pass', 'new-password', 'new-password')
    expect(await screen.findByText('Failed to change password')).toBeInTheDocument()
    fourth.unmount()

    vi.mocked(fetch).mockRejectedValueOnce('offline')
    render(<ChangePasswordPage userId={12} onChanged={onChanged} />)
    await fillPasswordForm('old-pass', 'new-password', 'new-password')
    expect(await screen.findByText('Failed to change password')).toBeInTheDocument()
  })

  it('accepts a password exactly at the eight-character boundary', async () => {
    const onChanged = vi.fn()
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(null, { status: 204 }))
    render(<ChangePasswordPage userId={21} onChanged={onChanged} />)
    await fillPasswordForm('old-pass', '12345678', '12345678')
    await waitFor(() => expect(onChanged).toHaveBeenCalledOnce())
    expect(fetch).toHaveBeenCalledWith('/api/users/21/password', expect.objectContaining({
      body: JSON.stringify({ old_password: 'old-pass', new_password: '12345678' }),
    }))
  })

  // A forced password change must not be submittable twice; the second request
  // would carry an old_password the server has already rotated away.
  it('locks the submit button until the change request settles', async () => {
    let release!: (value: Response) => void
    vi.spyOn(globalThis, 'fetch').mockReturnValue(new Promise<Response>((resolve) => { release = resolve }))
    render(<ChangePasswordPage userId={31} onChanged={vi.fn()} />)
    await fillPasswordForm('old-pass', 'new-password', 'new-password')

    const pending = await screen.findByRole('button', { name: 'Changing...' })
    expect(pending).toBeDisabled()
    fireEvent.click(pending)
    expect(fetch).toHaveBeenCalledTimes(1)

    await act(async () => { release(new Response(JSON.stringify({ error: 'wrong current password' }), { status: 401 })) })
    expect(await screen.findByText('wrong current password')).toBeInTheDocument()
    // The form must become usable again so the operator can correct and retry.
    expect(screen.getByRole('button', { name: 'Change Password' })).toBeEnabled()
  })
})
