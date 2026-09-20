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
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { getReferral, getReferralHistory } from '../api'
import { ReferralForm } from '../components/referral-form'
import { ReferralHistory } from '../components/referral-history'
import { RewardPreferenceForm } from '../components/reward-preference-form'
import { TeamUserSearch } from '../components/team-user-search'
import type { ReferralRelation, TeamSelf } from '../types'

const self: TeamSelf = {
  policy: {
    enabled: true,
    mode: 'credit',
    credit_rate_bps: 500,
    cash_rate_bps: 300,
    freeze_hours: 24,
    minimum_withdrawal_cents: 1000,
  },
  reward_preference: { user_id: 7, mode: 'cash', updated_at: 1 },
  effective_reward_mode: 'cash',
  wallet: { available_cents: 0, frozen_cents: 0, paid_cents: 0 },
  payout: { bound: false, account_masked: '', name_masked: '', updated_at: 0 },
  payout_ready: true,
  summary: {
    referred_users: 0,
    order_count: 0,
    pending_credit_quota: 0,
    settled_credit_quota: 0,
    pending_cash_cents: 0,
    settled_cash_cents: 0,
  },
}
const relation: ReferralRelation = {
  user_id: 7,
  username: 'member-seven',
  inviter_id: 8,
  inviter_username: 'original-referrer',
}
let client: QueryClient

beforeEach(() => {
  client = new QueryClient({
    defaultOptions: {
      mutations: { retry: false },
      queries: { retry: false, staleTime: Infinity },
    },
  })
})
afterEach(() => client.clear())

function renderTeam(element: ReactNode) {
  return render(
    <QueryClientProvider client={client}>{element}</QueryClientProvider>
  )
}

it('uses the personal reward choice instead of the policy default and saves only the mode', async () => {
  const user = userEvent.setup()
  const put = vi.spyOn(api, 'put').mockResolvedValue({
    data: {
      success: true,
      data: { user_id: 7, mode: 'credit', updated_at: 2 },
    },
  })
  renderTeam(<RewardPreferenceForm data={self} />)
  expect(
    screen.getByRole('button', { name: /Cash rewards.*3%/ })
  ).toHaveAttribute('aria-pressed', 'true')
  expect(
    screen.getByRole('button', { name: 'Save reward type' })
  ).toBeDisabled()
  await user.click(screen.getByRole('button', { name: /Site credits.*5%/ }))
  await user.click(screen.getByRole('button', { name: 'Save reward type' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledExactlyOnceWith(
      '/api/team/reward-preference',
      { mode: 'credit' },
      { skipBusinessError: true }
    )
  )
})

it('allows explicitly saving the inherited default but disables unavailable cash rewards', async () => {
  renderTeam(
    <RewardPreferenceForm
      data={{
        ...self,
        reward_preference: { user_id: 7, mode: '', updated_at: 0 },
        effective_reward_mode: 'credit',
        payout_ready: false,
      }}
    />
  )
  expect(screen.getByRole('button', { name: /Cash rewards/ })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Save reward type' })).toBeEnabled()
  expect(
    screen.getByText('Cash withdrawals are not configured yet.')
  ).toBeInTheDocument()
})

it('keeps a rejected reward choice unsaved and exposes the server error', async () => {
  const user = userEvent.setup()
  vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: false, message: 'Invalid reward preference' },
  })
  const invalidate = vi.spyOn(client, 'invalidateQueries')
  renderTeam(<RewardPreferenceForm data={self} />)
  await user.click(screen.getByRole('button', { name: /Site credits/ }))
  await user.click(screen.getByRole('button', { name: 'Save reward type' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Invalid reward preference'
  )
  expect(invalidate).not.toHaveBeenCalled()
})

it('ignores repeated reward preference submits while the first save is pending', async () => {
  const user = userEvent.setup()
  const put = vi
    .spyOn(api, 'put')
    .mockImplementation(() => new Promise(() => {}))
  renderTeam(<RewardPreferenceForm data={self} />)
  await user.click(screen.getByRole('button', { name: /Site credits/ }))
  const button = screen.getByRole('button', { name: 'Save reward type' })
  const form = button.closest('form')
  expect(form).not.toBeNull()
  if (!form) throw new Error('Expected the reward preference form')
  await act(async () => {
    fireEvent.submit(form)
    fireEvent.submit(form)
  })
  expect(put).toHaveBeenCalledOnce()
})

it('reads the current referral and audit page through the administrator endpoints', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: relation } })
  expect(await getReferral(7)).toEqual(relation)
  await getReferralHistory(7, 2)
  expect(get).toHaveBeenNthCalledWith(1, '/api/team/admin/referrals/7', {
    skipBusinessError: true,
  })
  expect(get).toHaveBeenNthCalledWith(2, '/api/team/admin/referrals/7/audits', {
    params: { p: 2, page_size: 20 },
    skipBusinessError: true,
  })
})

it('requires a changed relationship and a nonblank reason of at most 200 characters', async () => {
  const user = userEvent.setup()
  const put = vi.spyOn(api, 'put')
  renderTeam(
    <ReferralForm
      relation={relation}
      onBusyChange={vi.fn()}
      onRefresh={vi.fn()}
    />
  )
  expect(
    screen.getByRole('button', { name: 'Review relationship change' })
  ).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Remove relationship' }))
  await user.click(
    screen.getByRole('button', { name: 'Review relationship change' })
  )
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Enter a reason (1-200 characters).'
  )
  await user.type(screen.getByLabelText('Reason for change'), 'x'.repeat(201))
  await user.click(
    screen.getByRole('button', { name: 'Review relationship change' })
  )
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(put).not.toHaveBeenCalled()
})

async function confirmUnbind() {
  const user = userEvent.setup()
  const onBusyChange = vi.fn()
  const onRefresh = vi.fn()
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        scope: 'team.referral.write',
        methods: [{ method: '2fa', available: true }],
        oauth_providers: [],
        password_encryption_enabled: false,
      },
    },
  })
  renderTeam(
    <ReferralForm
      relation={relation}
      onBusyChange={onBusyChange}
      onRefresh={onRefresh}
    />
  )
  await user.click(screen.getByRole('button', { name: 'Remove relationship' }))
  await user.type(
    screen.getByLabelText('Reason for change'),
    '  Requested by member  '
  )
  await user.click(
    screen.getByRole('button', { name: 'Review relationship change' })
  )
  const confirmation = screen.getByRole('alertdialog')
  expect(within(confirmation).getByText(/member-seven/)).toBeInTheDocument()
  expect(
    within(confirmation).getByText(/original-referrer/)
  ).toBeInTheDocument()
  expect(within(confirmation).getByText(/Not linked/)).toBeInTheDocument()
  await user.click(
    within(confirmation).getByRole('button', { name: 'Confirm and verify' })
  )
  await screen.findByLabelText('Authenticator code or backup code')
  return { user, onBusyChange, onRefresh }
}

it('cancels referral verification without changing the relationship', async () => {
  const put = vi.spyOn(api, 'put')
  const post = vi.spyOn(api, 'post')
  const { user, onBusyChange } = await confirmUnbind()
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  await waitFor(() => expect(onBusyChange).toHaveBeenLastCalledWith(false))
  expect(put).not.toHaveBeenCalled()
  expect(post).not.toHaveBeenCalled()
})

it.each([true, false])(
  'binds referral verification to old/new IDs and reason; success=%s',
  async (success) => {
    const context = {
      user_id: 7,
      expected_inviter_id: 8,
      inviter_id: 0,
      reason: 'Requested by member',
    }
    const post = vi.spyOn(api, 'post').mockResolvedValue({
      data: {
        success: true,
        data: {
          proof_token: 'referral-proof',
          scope: 'team.referral.write',
          method: '2fa',
          expires_at: Math.floor(Date.now() / 1000) + 60,
        },
      },
    })
    const put = vi.spyOn(api, 'put').mockResolvedValue({
      data: success
        ? { success: true }
        : {
            success: false,
            message:
              'The referral relationship changed. Refresh and try again.',
          },
    })
    const { user, onBusyChange, onRefresh } = await confirmUnbind()
    await user.type(
      screen.getByLabelText('Authenticator code or backup code'),
      '123456'
    )
    await user.click(screen.getByRole('button', { name: 'Verify' }))
    await waitFor(() =>
      expect(post).toHaveBeenCalledExactlyOnceWith(
        '/api/verify',
        {
          scope: 'team.referral.write',
          context,
          method: '2fa',
          code: '123456',
        },
        expect.objectContaining({
          skipBusinessError: true,
          skipErrorHandler: true,
        })
      )
    )
    await waitFor(() =>
      expect(put).toHaveBeenCalledExactlyOnceWith(
        '/api/team/admin/referrals/7',
        {
          expected_inviter_id: 8,
          inviter_id: 0,
          reason: 'Requested by member',
        },
        {
          skipBusinessError: true,
          singleUseAuthorization: true,
          headers: { 'X-Security-Proof': 'referral-proof' },
        }
      )
    )
    await waitFor(() => expect(onBusyChange).toHaveBeenLastCalledWith(false))
    if (!success) {
      expect(await screen.findByRole('alert')).toHaveTextContent(
        'The referral relationship changed. Refresh and try again.'
      )
      await user.click(screen.getByRole('button', { name: 'Refresh' }))
      expect(onRefresh).toHaveBeenCalledOnce()
    }
  }
)

it('searches candidates by username, disables self-selection, and supports paging', async () => {
  const user = userEvent.setup()
  const onSelect = vi.fn()
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        items: [
          { id: 7, username: 'member-seven', display_name: 'Member' },
          { id: 9, username: 'candidate-nine', display_name: 'Candidate' },
        ],
        total: 21,
        page: 1,
        page_size: 20,
      },
    },
  })
  renderTeam(
    <TeamUserSearch
      label='Find a referrer'
      action='Select referrer'
      excludedID={7}
      onSelect={onSelect}
    />
  )
  await screen.findByText('member-seven')
  const rows = screen.getAllByRole('row')
  expect(
    within(rows[1]).getByRole('button', { name: 'Select referrer' })
  ).toBeDisabled()
  await user.click(
    within(rows[2]).getByRole('button', { name: 'Select referrer' })
  )
  expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: 9 }))
  await user.type(screen.getByLabelText('Find a referrer'), 'candidate-nine')
  await user.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith(
      '/api/user/search?keyword=candidate-nine&group=&p=1&page_size=20'
    )
  )
  await user.click(screen.getByRole('button', { name: 'Next page' }))
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith(
      '/api/user/search?keyword=candidate-nine&group=&p=2&page_size=20'
    )
  )
})

it('shows paged relationship audit entries and the empty history state', async () => {
  const user = userEvent.setup()
  client.setQueryData(['team', 'referral-history', 7, 1], {
    items: [
      {
        id: 1,
        user_id: 7,
        actor_id: 1,
        previous_inviter_id: 8,
        inviter_id: 0,
        reason: 'member requested unbind',
        created_at: 1700000000,
      },
    ],
    total: 21,
    page: 1,
    page_size: 20,
  })
  client.setQueryData(['team', 'referral-history', 7, 2], {
    items: [],
    total: 21,
    page: 2,
    page_size: 20,
  })
  renderTeam(<ReferralHistory userID={7} />)
  expect(screen.getByText('member requested unbind')).toBeInTheDocument()
  expect(screen.getByText('Not linked')).toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Next page' }))
  expect(screen.getByText('No records yet')).toBeInTheDocument()
})
