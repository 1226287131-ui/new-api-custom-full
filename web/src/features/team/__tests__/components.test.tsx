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
import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { CommissionTable } from '../components/commission-table'
import { PayoutForm } from '../components/payout-form'
import { PolicyForm } from '../components/policy-form'
import { TeamError, TeamLoading } from '../components/shared'
import { WithdrawalForm } from '../components/withdrawal-form'
import type { Commission, TeamPolicy, TeamSelf } from '../types'

const policy: TeamPolicy = {
  enabled: true,
  mode: 'credit',
  credit_rate_bps: 500,
  cash_rate_bps: 500,
  freeze_hours: 24,
  minimum_withdrawal_cents: 1000,
}
const self: TeamSelf = {
  policy,
  reward_preference: { user_id: 7, mode: '', updated_at: 0 },
  effective_reward_mode: 'credit',
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
const commission: Commission = {
  id: 12,
  user_id: 8,
  referrer_id: 7,
  trade_no: 'PRIVATE-ORDER',
  mode: 'credit',
  rate_bps: 500,
  paid_cents: 10000,
  credit_quota: 25000000,
  cash_cents: 0,
  quota_per_unit: '500000',
  status: 'settled',
  created_at: 1700000000,
  ready_at: 1700000000,
  settled_at: 1700000000,
}
let client: QueryClient

function renderTeam(element: ReactNode) {
  return render(element, {
    wrapper: (props: { children: ReactNode }) => (
      <QueryClientProvider client={client}>
        {props.children}
      </QueryClientProvider>
    ),
  })
}

describe('Team account controls', () => {
  beforeEach(() => {
    client = new QueryClient({
      defaultOptions: {
        queries: { retry: false, staleTime: Infinity, gcTime: Infinity },
        mutations: { retry: false },
      },
    })
  })
  afterEach(() => {
    client.clear()
  })

  test('personal reward records show snapshotted credits without exposing payment order numbers', async () => {
    client.setQueryData(['team', 'commissions', undefined, 1], {
      items: [commission],
      total: 1,
      page: 1,
      page_size: 20,
    })
    renderTeam(<CommissionTable />)
    expect(screen.getByText('50 site credits')).toBeInTheDocument()
    expect(screen.getByText('Record ID')).toBeInTheDocument()
    expect(screen.queryByText('PRIVATE-ORDER')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Previous page' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Next page' })).toBeDisabled()
  })

  test('administrator records expose order numbers and paging advances through independent results', async () => {
    client.setQueryData(['team', 'commissions', true, 1], {
      items: [commission],
      total: 21,
      page: 1,
      page_size: 20,
    })
    client.setQueryData(['team', 'commissions', true, 2], {
      items: [{ ...commission, id: 13, trade_no: 'SECOND-PAGE' }],
      total: 21,
      page: 2,
      page_size: 20,
    })
    const user = userEvent.setup()
    renderTeam(<CommissionTable admin />)
    expect(screen.getByText('PRIVATE-ORDER')).toBeInTheDocument()
    const next = screen.getByRole('button', { name: 'Next page' })
    expect(next).toBeEnabled()
    await user.click(next)
    expect(screen.getByText('SECOND-PAGE')).toBeInTheDocument()
    expect(next).toBeDisabled()
  })

  test('empty records show an explicit empty state with disabled paging', async () => {
    client.setQueryData(['team', 'commissions', undefined, 1], {
      items: [],
      total: 0,
      page: 1,
      page_size: 20,
    })
    renderTeam(<CommissionTable />)
    expect(screen.getByText('No records yet')).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Next page' })).toBeDisabled()
  })

  test('loading has an accessible status and errors provide a retry action', async () => {
    const user = userEvent.setup()
    const view = renderTeam(<TeamLoading />)
    expect(screen.getByRole('status', { name: 'Loading' })).toBeInTheDocument()
    const retry = vi.fn()
    view.rerender(<TeamError retry={retry} />)
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(retry).toHaveBeenCalledOnce()
  })

  test('non-root administrators can inspect policy but cannot change or save it', async () => {
    renderTeam(<PolicyForm policy={policy} writable={false} />)
    for (const input of screen.getAllByRole('textbox')) {
      expect(input).toBeDisabled()
    }
    expect(
      screen.queryByRole('button', { name: 'Save reward settings' })
    ).not.toBeInTheDocument()
    expect(screen.getByText(/Only the root administrator/)).toBeInTheDocument()
  })

  test('reward policy ignores repeated submits while the first save is pending', async () => {
    const put = vi
      .spyOn(api, 'put')
      .mockImplementation(() => new Promise(() => {}))
    renderTeam(<PolicyForm policy={policy} writable />)
    const button = screen.getByRole('button', { name: 'Save reward settings' })
    const form = button.closest('form')
    expect(form).not.toBeNull()
    if (!form) throw new Error('Expected the reward policy form')
    await act(async () => {
      fireEvent.submit(form)
      fireEvent.submit(form)
    })
    expect(put).toHaveBeenCalledOnce()
  })

  test('payout binding displays masked data without pre-filling sensitive credentials', async () => {
    renderTeam(<PayoutForm data={self} />)
    expect(screen.getByText(/p\*\*\*@example.test/)).toBeInTheDocument()
    expect(screen.getByLabelText('Alipay account')).toHaveValue('')
    expect(screen.getByLabelText('Account holder name')).toHaveValue('')
  })

  test('withdrawals are unavailable while disabled, unbound, unconfigured or below the minimum', async () => {
    const cases = [
      { ...self, policy: { ...policy, enabled: false } },
      { ...self, payout: { ...self.payout, bound: false } },
      { ...self, payout_ready: false },
      { ...self, wallet: { ...self.wallet, available_cents: 999 } },
    ]
    const view = renderTeam(<WithdrawalForm data={self} />)
    for (const data of cases) {
      view.rerender(<WithdrawalForm data={data} />)
      expect(screen.getByLabelText('Withdrawal amount (CNY)')).toBeDisabled()
      expect(
        screen.getByRole('button', { name: 'Request withdrawal' })
      ).toBeDisabled()
    }
    view.rerender(<WithdrawalForm data={self} />)
    expect(screen.getByLabelText('Withdrawal amount (CNY)')).toBeEnabled()
  })
})
