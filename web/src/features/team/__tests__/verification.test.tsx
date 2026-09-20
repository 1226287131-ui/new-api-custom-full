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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { PayoutForm } from '../components/payout-form'
import { WithdrawalForm } from '../components/withdrawal-form'
import { WithdrawalReviewDialog } from '../components/withdrawal-review-dialog'
import type { TeamSelf, Withdrawal } from '../types'

const self: TeamSelf = {
  reward_preference: { user_id: 7, mode: '', updated_at: 0 },
  effective_reward_mode: 'cash',
  policy: {
    enabled: true,
    mode: 'cash',
    credit_rate_bps: 500,
    cash_rate_bps: 500,
    freeze_hours: 24,
    minimum_withdrawal_cents: 1000,
  },
  wallet: { available_cents: 2000, frozen_cents: 0, paid_cents: 0 },
  payout: {
    bound: true,
    account_masked: 'p***@example.test',
    name_masked: 'L*',
    updated_at: 100,
  },
  payout_ready: true,
  summary: {
    referred_users: 1,
    order_count: 1,
    pending_credit_quota: 0,
    settled_credit_quota: 0,
    pending_cash_cents: 0,
    settled_cash_cents: 0,
  },
}
const withdrawal: Withdrawal = {
  id: 7,
  user_id: 8,
  request_id: 'withdrawal-7',
  amount_cents: 1234,
  status: 'pending',
  account_masked: 'p***@example.test',
  name_masked: 'L*',
  created_at: 1700000000,
  reviewed_at: 0,
  paid_at: 0,
  payment_reference: '',
  rejection_reason: '',
}
const operations = [
  { kind: 'payout', scope: 'team.payout.write', button: 'Save payout account' },
  {
    kind: 'withdraw',
    scope: 'team.withdrawal.write',
    button: 'Request withdrawal',
  },
  {
    kind: 'review',
    scope: 'team.withdrawal.review',
    button: 'Approve withdrawal',
  },
  {
    kind: 'reveal',
    scope: 'team.withdrawal.read',
    button: 'Reveal payout account',
  },
] as const
type Operation = (typeof operations)[number]
let client: QueryClient

beforeEach(() => {
  client = new QueryClient({
    defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
  })
  vi.spyOn(api, 'get').mockImplementation(async (_url, config) => ({
    data: {
      success: true,
      data: {
        scope: config?.params.scope,
        methods: [{ method: '2fa', available: true }],
        oauth_providers: [],
        password_encryption_enabled: false,
      },
    },
  }))
})
afterEach(() => client.clear())

async function beginOperation(operation: Operation) {
  const user = userEvent.setup()
  const onClose = vi.fn()
  let element: ReactNode
  if (operation.kind === 'payout') {
    element = <PayoutForm data={self} />
  } else if (operation.kind === 'withdraw') {
    element = <WithdrawalForm data={self} />
  } else {
    element = <WithdrawalReviewDialog item={withdrawal} onClose={onClose} />
  }
  render(<QueryClientProvider client={client}>{element}</QueryClientProvider>)
  if (operation.kind === 'payout') {
    await user.type(screen.getByLabelText('Alipay account'), 'pay@example.test')
    await user.type(screen.getByLabelText('Account holder name'), 'Li Ming')
  } else if (operation.kind === 'withdraw') {
    await user.type(screen.getByLabelText('Withdrawal amount (CNY)'), '12.34')
  }
  await user.click(screen.getByRole('button', { name: operation.button }))
  await screen.findByLabelText('Authenticator code or backup code')
  return { user, onClose }
}

it.each(operations)(
  'does not send the $kind operation when verification is cancelled',
  async (operation) => {
    const post = vi.spyOn(api, 'post')
    const put = vi.spyOn(api, 'put')
    const { user, onClose } = await beginOperation(operation)
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: operation.button })
      ).toBeEnabled()
    )
    expect(post).not.toHaveBeenCalled()
    expect(put).not.toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()
  }
)

it.each(operations)(
  'sends the verified proof token exactly once for the $kind operation',
  async (operation) => {
    const post = vi.spyOn(api, 'post').mockImplementation(async (url, body) => {
      if (url === '/api/verify') {
        expect(body).toEqual({
          scope: operation.scope,
          method: '2fa',
          code: '123456',
        })
        return {
          data: {
            success: true,
            data: {
              proof_token: 'team-proof',
              scope: operation.scope,
              method: '2fa',
              expires_at: Math.floor(Date.now() / 1000) + 60,
            },
          },
        }
      }
      return {
        data: {
          success: true,
          data: { account: 'pay@example.test', name: 'Li Ming' },
        },
      }
    })
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    const { user, onClose } = await beginOperation(operation)
    await user.type(
      screen.getByLabelText('Authenticator code or backup code'),
      '123456'
    )
    await user.click(screen.getByRole('button', { name: 'Verify' }))
    const options = expect.objectContaining({
      headers: { 'X-Security-Proof': 'team-proof' },
      singleUseAuthorization: true,
    })
    if (operation.kind === 'payout') {
      await waitFor(() =>
        expect(put).toHaveBeenCalledExactlyOnceWith(
          '/api/team/payout',
          { account: 'pay@example.test', name: 'Li Ming' },
          options
        )
      )
      expect(post).toHaveBeenCalledTimes(1)
    } else {
      let endpoint: string
      let body: unknown
      if (operation.kind === 'withdraw') {
        endpoint = '/api/team/withdrawals'
        body = { amount_cents: 1234, request_id: expect.any(String) }
      } else if (operation.kind === 'review') {
        endpoint = '/api/team/admin/withdrawals/7/review'
        body = { action: 'approve', reference: '', reason: '' }
      } else {
        endpoint = '/api/team/admin/withdrawals/7/payout'
        body = {}
      }
      await waitFor(() =>
        expect(post).toHaveBeenCalledWith(endpoint, body, options)
      )
      expect(post).toHaveBeenCalledTimes(2)
      expect(put).not.toHaveBeenCalled()
    }
    if (operation.kind === 'review') {
      await waitFor(() => expect(onClose).toHaveBeenCalledOnce())
    }
    if (operation.kind === 'reveal') {
      expect(await screen.findByText(/pay@example.test/)).toBeInTheDocument()
    }
  }
)
