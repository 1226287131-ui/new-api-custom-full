/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import type { AuthBundle } from '@/stores/auth-store'

import { SecureVerificationDialog } from '../components/secure-verification-dialog'
import { useSecureVerification } from '../hooks/use-secure-verification'
import type {
  RequestVerificationOptions,
  SecurityProof,
  VerificationRequirements,
} from '../types'

const passwordRequirements: VerificationRequirements = {
  scope: 'passkey.register',
  methods: [{ method: 'password', available: true }],
  oauth_providers: [],
  password_encryption_enabled: false,
}

function Harness(props: {
  onResult: (proof: SecurityProof | null) => void
  operation?: RequestVerificationOptions
}) {
  const verification = useSecureVerification()
  const [client] = useState(
    () => new QueryClient({ defaultOptions: { queries: { retry: false } } })
  )
  return (
    <QueryClientProvider client={client}>
      <button
        type='button'
        onClick={async () =>
          props.onResult(
            await verification.requestVerification(
              props.operation ?? {
                scope: 'passkey.register',
              }
            )
          )
        }
      >
        Protected action
      </button>
      <SecureVerificationDialog {...verification.dialogProps} />
    </QueryClientProvider>
  )
}

function pendingResponse<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((finish) => {
    resolve = finish
  })
  return { promise, resolve }
}

async function renderTeamVerification(
  operation: RequestVerificationOptions,
  onResult: (proof: SecurityProof | null) => void
) {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined)
  const rootRoute = createRootRoute({ component: Outlet })
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/',
    component: () => <Harness operation={operation} onResult={onResult} />,
  })
  const securityRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/security',
    component: () => <h1>Personal security settings</h1>,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute, securityRoute]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  render(<RouterProvider router={router} />)
  return router
}

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function LoginHarness(props: {
  onResult: (bundle: AuthBundle | null) => void
}) {
  const verification = useSecureVerification()
  const [client] = useState(
    () => new QueryClient({ defaultOptions: { queries: { retry: false } } })
  )
  return (
    <QueryClientProvider client={client}>
      <button
        type='button'
        onClick={async () =>
          props.onResult(
            await verification.requestLoginVerification({
              require_verification: true,
              flow_token: 'pending-login',
              expires_at: Math.floor(Date.now() / 1000) + 300,
              methods: [
                { method: '2fa', available: true },
                { method: 'passkey', available: true },
              ],
            })
          )
        }
      >
        Continue sign-in
      </button>
      <SecureVerificationDialog {...verification.dialogProps} />
    </QueryClientProvider>
  )
}

it('lets a pending login switch from Passkey to 2FA without using authenticated verification endpoints', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: {} } })
  vi.stubGlobal('PublicKeyCredential', class {})
  const bundle: AuthBundle = {
    access_token: 'verified-login',
    token_type: 'Bearer',
    access_expires_at: Math.floor(Date.now() / 1000) + 900,
    user: { id: 42, username: 'user', role: 1 },
    session: {
      sid: 'new-session',
      current: true,
      login_method: 'password',
      ip: '',
      user_agent: '',
      created_at: 1,
      last_active_at: 1,
      expires_at: Math.floor(Date.now() / 1000) + 3600,
    },
  }
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, data: bundle } })
  const result = vi.fn()
  const user = userEvent.setup()
  render(<LoginHarness onResult={result} />)
  await user.click(screen.getByRole('button', { name: 'Continue sign-in' }))
  expect(await screen.findByRole('tab', { name: 'Passkey' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  expect(result).not.toHaveBeenCalled()
  await user.click(screen.getByRole('tab', { name: 'Authenticator code' }))
  await user.type(
    screen.getByLabelText('Authenticator code or backup code'),
    '123456'
  )
  await user.click(screen.getByRole('button', { name: 'Verify' }))
  await waitFor(() => expect(result).toHaveBeenCalledExactlyOnceWith(bundle))
  expect(post).toHaveBeenCalledExactlyOnceWith(
    '/api/user/login/verify',
    { flow_token: 'pending-login', method: '2fa', code: '123456' },
    expect.objectContaining({
      skipAuthRefresh: true,
      signal: expect.any(AbortSignal),
    })
  )
  expect(get.mock.calls.every(([url]) => url === '/api/status')).toBe(true)
})

it.each(['success', 'cancel', 'retry'] as const)(
  'automatically obtains the first-enrollment session proof and handles %s',
  async (outcome) => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          ...passwordRequirements,
          methods: [{ method: 'session', available: true }],
        },
      },
    })
    const proof: SecurityProof = {
      proof_token: 'session-proof',
      method: 'session',
      scope: 'passkey.register',
      expires_at: Math.floor(Date.now() / 1000) + 300,
    }
    const reply = pendingResponse<{
      data: { success: boolean; data: SecurityProof }
    }>()
    const post = vi.spyOn(api, 'post').mockReturnValue(reply.promise)
    if (outcome === 'retry') {
      post.mockRejectedValueOnce(new Error('Session check failed'))
    }
    const result = vi.fn()
    const user = userEvent.setup()
    render(<Harness onResult={result} />)
    await user.click(screen.getByText('Protected action'))
    if (outcome === 'retry') {
      expect(await screen.findByRole('alert')).toHaveTextContent(
        'Session check failed'
      )
      await user.click(screen.getByRole('button', { name: 'Retry' }))
    }
    await waitFor(() =>
      expect(post).toHaveBeenCalledTimes(outcome === 'retry' ? 2 : 1)
    )
    expect(post).toHaveBeenLastCalledWith(
      '/api/verify',
      { method: 'session', scope: 'passkey.register' },
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    )
    expect(screen.getByRole('button', { name: 'Verify' })).toBeDisabled()
    if (outcome === 'cancel') {
      await user.click(screen.getByRole('button', { name: 'Cancel' }))
      expect(post.mock.calls[0][2]?.signal?.aborted).toBe(true)
    }
    await act(async () => {
      reply.resolve({ data: { success: true, data: proof } })
      await reply.promise
    })
    await waitFor(() =>
      expect(result).toHaveBeenCalledExactlyOnceWith(
        outcome === 'cancel' ? null : proof
      )
    )
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  }
)

it('keeps the requested channel context fixed while verification is open', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        scope: 'channel.key.read',
        methods: [{ method: '2fa', available: true }],
        oauth_providers: [],
        password_encryption_enabled: false,
      },
    },
  })
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        proof_token: 'channel-proof',
        scope: 'channel.key.read',
        method: '2fa',
        expires_at: Math.floor(Date.now() / 1000) + 60,
      },
    },
  })
  const operation: RequestVerificationOptions = {
    scope: 'channel.key.read',
    context: { channel_id: 123 },
  }
  const user = userEvent.setup()
  const result = vi.fn()
  render(<Harness operation={operation} onResult={result} />)
  await user.click(screen.getByText('Protected action'))
  await user.type(
    await screen.findByLabelText('Authenticator code or backup code'),
    '123456'
  )
  operation.context.channel_id = 456
  await user.click(screen.getByRole('button', { name: 'Verify' }))
  await waitFor(() => expect(result).toHaveBeenCalledTimes(1))
  expect(post).toHaveBeenCalledWith(
    '/api/verify',
    {
      scope: 'channel.key.read',
      context: { channel_id: 123 },
      method: '2fa',
      code: '123456',
    },
    expect.anything()
  )
})

it('shows query errors and reloads requirements only when the user retries', async () => {
  const requests = vi
    .spyOn(api, 'get')
    .mockRejectedValueOnce(new Error('Unable to load methods'))
    .mockResolvedValue({ data: { success: true, data: passwordRequirements } })
  const result = vi.fn()
  const user = userEvent.setup()
  render(<Harness onResult={result} />)
  await user.click(screen.getByText('Protected action'))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Unable to load methods'
  )
  expect(
    screen.queryByText('No verification method is available for this action.')
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Retry' }))
  expect(
    await screen.findByLabelText('Password', { selector: 'input' })
  ).toBeVisible()
  expect(requests).toHaveBeenCalledTimes(2)
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  await waitFor(() => expect(result).toHaveBeenCalledWith(null))
})

it('returns a proof only after successful verification and clears a rejected password', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: passwordRequirements },
  })
  const proof: SecurityProof = {
    proof_token: 'proof',
    method: 'password',
    scope: 'passkey.register',
    expires_at: Math.floor(Date.now() / 1000) + 300,
  }
  const posts = vi
    .spyOn(api, 'post')
    .mockResolvedValueOnce({
      data: { success: false, message: 'Wrong password' },
    })
    .mockResolvedValueOnce({ data: { success: true, data: proof } })
  const result = vi.fn()
  const user = userEvent.setup()
  render(<Harness onResult={result} />)
  await user.click(screen.getByText('Protected action'))
  await user.type(
    await screen.findByLabelText('Password', { selector: 'input' }),
    'incorrect'
  )
  await user.click(screen.getByRole('button', { name: 'Verify' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('Wrong password')
  expect(screen.getByLabelText('Password', { selector: 'input' })).toHaveValue(
    ''
  )
  expect(result).not.toHaveBeenCalled()
  await user.type(
    screen.getByLabelText('Password', { selector: 'input' }),
    'correct'
  )
  await user.keyboard('{Enter}')
  await waitFor(() => expect(result).toHaveBeenCalledWith(proof))
  expect(posts).toHaveBeenLastCalledWith(
    '/api/verify',
    { method: 'password', scope: 'passkey.register', password: 'correct' },
    expect.objectContaining({ signal: expect.any(AbortSignal) })
  )
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

it('discards a late proof after cancellation and prevents a duplicate submission', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: passwordRequirements },
  })
  const reply = pendingResponse<{
    data: { success: boolean; data: SecurityProof }
  }>()
  const posts = vi.spyOn(api, 'post').mockReturnValue(reply.promise)
  const result = vi.fn()
  const user = userEvent.setup()
  render(<Harness onResult={result} />)
  await user.click(screen.getByText('Protected action'))
  await user.type(
    await screen.findByLabelText('Password', { selector: 'input' }),
    'password'
  )
  const submit = screen.getByRole('button', { name: 'Verify' })
  fireEvent.click(submit)
  fireEvent.click(submit)
  await waitFor(() => expect(posts).toHaveBeenCalledTimes(1))
  expect(submit).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  await waitFor(() => expect(result).toHaveBeenCalledExactlyOnceWith(null))
  await act(async () => {
    reply.resolve({
      data: {
        success: true,
        data: {
          proof_token: 'late-proof',
          method: 'password',
          scope: 'passkey.register',
          expires_at: Math.floor(Date.now() / 1000) + 300,
        },
      },
    })
    await reply.promise
  })
  expect(result).toHaveBeenCalledTimes(1)
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

it('does not reopen after a cancelled method query finishes', async () => {
  const reply = pendingResponse<{
    data: { success: boolean; data: VerificationRequirements }
  }>()
  vi.spyOn(api, 'get').mockReturnValue(reply.promise)
  const result = vi.fn()
  const user = userEvent.setup()
  render(<Harness onResult={result} />)
  await user.click(screen.getByText('Protected action'))
  expect(screen.getByRole('status')).toHaveTextContent(
    'Loading verification methods...'
  )
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  await act(async () => {
    reply.resolve({ data: { success: true, data: passwordRequirements } })
    await reply.promise
  })
  expect(result).toHaveBeenCalledExactlyOnceWith(null)
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

it('keeps an enrolled Passkey unavailable when this browser cannot use it', async () => {
  vi.stubGlobal('PublicKeyCredential', undefined)
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        ...passwordRequirements,
        methods: [{ method: 'passkey', available: true }],
      },
    },
  })
  const user = userEvent.setup()
  render(<Harness onResult={vi.fn()} />)
  await user.click(screen.getByText('Protected action'))
  expect(
    await screen.findByText(
      'This device does not support Passkey verification.'
    )
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Verify' })).toBeDisabled()
  expect(screen.queryByLabelText('Password')).not.toBeInTheDocument()
})

it('takes an unenrolled team payout user to personal security settings without completing the action', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        ...passwordRequirements,
        scope: 'team.payout.write',
        methods: [],
      },
    },
  })
  const post = vi.spyOn(api, 'post')
  const result = vi.fn()
  const user = userEvent.setup()
  const router = await renderTeamVerification(
    { scope: 'team.payout.write' },
    result
  )
  await user.click(screen.getByText('Protected action'))
  expect(
    await screen.findByText(
      'Enable Two-factor Authentication or Passkey in Security & Access to continue.'
    )
  ).toBeVisible()
  const settings = screen.getByRole('button', {
    name: 'Open Security & Access',
  })
  expect(settings).toHaveAttribute('href', '/security')
  expect(
    screen.queryByRole('button', { name: 'Verify' })
  ).not.toBeInTheDocument()
  expect(result).not.toHaveBeenCalled()
  settings.focus()
  await user.keyboard('{Enter}')
  await screen.findByRole('heading', { name: 'Personal security settings' })
  expect(router.state.location.pathname).toBe('/security')
  expect(result).toHaveBeenCalledExactlyOnceWith(null)
  expect(post).not.toHaveBeenCalled()
})

it.each([
  { method: '2fa', reason: 'Two-factor authentication is temporarily locked.' },
  { method: 'passkey', reason: 'Passkey authentication is disabled.' },
  {
    method: 'passkey',
    reason: 'This device does not support Passkey verification.',
  },
] as const)(
  'preserves the unavailable $method reason instead of treating enrollment as missing: $reason',
  async (option) => {
    vi.spyOn(api, 'get').mockResolvedValue({
      data: {
        success: true,
        data: {
          ...passwordRequirements,
          scope: 'team.withdrawal.write',
          methods: [{ ...option, available: false }],
        },
      },
    })
    const post = vi.spyOn(api, 'post')
    const result = vi.fn()
    const user = userEvent.setup()
    await renderTeamVerification({ scope: 'team.withdrawal.write' }, result)
    await user.click(screen.getByText('Protected action'))
    expect(await screen.findByText(option.reason)).toBeVisible()
    expect(
      screen.queryByText(
        'Enable Two-factor Authentication or Passkey in Security & Access to continue.'
      )
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Open Security & Access' })
    ).toHaveAttribute('href', '/security')
    expect(result).not.toHaveBeenCalled()
    expect(post).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(result).toHaveBeenCalledExactlyOnceWith(null)
  }
)

it('retains the existing unavailable-method state for non-team actions', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { ...passwordRequirements, methods: [] } },
  })
  const result = vi.fn()
  const user = userEvent.setup()
  render(<Harness onResult={result} />)
  await user.click(screen.getByText('Protected action'))
  expect(
    await screen.findByText(
      'No verification method is available for this action.'
    )
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Verify' })).toBeDisabled()
  expect(
    screen.queryByRole('button', { name: 'Open Security & Access' })
  ).not.toBeInTheDocument()
  expect(result).not.toHaveBeenCalled()
})
