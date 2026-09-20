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
import assert from 'node:assert/strict'
import { after, afterEach, beforeEach, describe, test } from 'node:test'

import { Window } from 'happy-dom'
import type { ReactNode } from 'react'

import type { Commission, TeamPolicy, TeamSelf } from '../types'

const domWindow = new Window({ url: 'http://localhost' })
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const
const previousGlobals = domGlobals.map(
  (key) => [key, Object.getOwnPropertyDescriptor(globalThis, key)] as const
)
for (const key of domGlobals) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { CommissionTable } = await import('../components/commission-table')
const { PolicyForm } = await import('../components/policy-form')
const { PayoutForm } = await import('../components/payout-form')
const { WithdrawalForm } = await import('../components/withdrawal-form')
const { TeamError, TeamLoading } = await import('../components/shared')

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
const reactGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
const previousAct = reactGlobals.IS_REACT_ACT_ENVIRONMENT
reactGlobals.IS_REACT_ACT_ENVIRONMENT = true

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
const originalAdapter = api.defaults.adapter
let container: HTMLDivElement
let root: ReturnType<typeof createRoot>
let client: InstanceType<typeof QueryClient>

async function renderTeam(element: ReactNode) {
  await act(async () =>
    root.render(
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={client}>{element}</QueryClientProvider>
      </I18nextProvider>
    )
  )
}

describe('Team account controls', () => {
  beforeEach(() => {
    container = document.createElement('div')
    document.body.append(container)
    root = createRoot(container)
    client = new QueryClient({
      defaultOptions: {
        queries: { retry: false, staleTime: Infinity, gcTime: Infinity },
        mutations: { retry: false },
      },
    })
    api.defaults.adapter = async (config) => ({
      data: { success: true, data: { enabled: false } },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    })
  })
  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    client.clear()
    api.defaults.adapter = originalAdapter
  })
  after(() => {
    domWindow.close()
    for (const [key, descriptor] of previousGlobals) {
      if (descriptor) Object.defineProperty(globalThis, key, descriptor)
      else Reflect.deleteProperty(globalThis, key)
    }
    reactGlobals.IS_REACT_ACT_ENVIRONMENT = previousAct
  })

  test('personal reward records show snapshotted credits without exposing payment order numbers', async () => {
    client.setQueryData(['team', 'commissions', undefined, 1], {
      items: [commission],
      total: 1,
      page: 1,
      page_size: 20,
    })
    await renderTeam(<CommissionTable />)
    assert.ok(container.textContent?.includes('50 site credits'))
    assert.ok(container.textContent?.includes('Record ID'))
    assert.equal(container.textContent?.includes('PRIVATE-ORDER'), false)
    assert.equal(
      container.querySelector<HTMLButtonElement>('[aria-label="Previous page"]')
        ?.disabled,
      true
    )
    assert.equal(
      container.querySelector<HTMLButtonElement>('[aria-label="Next page"]')
        ?.disabled,
      true
    )
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
    await renderTeam(<CommissionTable admin />)
    assert.ok(container.textContent?.includes('PRIVATE-ORDER'))
    const next = container.querySelector<HTMLButtonElement>(
      '[aria-label="Next page"]'
    )
    assert.ok(next)
    assert.equal(next.disabled, false)
    await act(async () => next.click())
    assert.ok(container.textContent?.includes('SECOND-PAGE'))
    assert.equal(next.disabled, true)
  })

  test('empty records show an explicit empty state with disabled paging', async () => {
    client.setQueryData(['team', 'commissions', undefined, 1], {
      items: [],
      total: 0,
      page: 1,
      page_size: 20,
    })
    await renderTeam(<CommissionTable />)
    assert.ok(container.textContent?.includes('No records yet'))
    assert.equal(container.querySelector('table'), null)
    assert.equal(
      container.querySelector<HTMLButtonElement>('[aria-label="Next page"]')
        ?.disabled,
      true
    )
  })

  test('loading has an accessible status and errors provide a retry action', async () => {
    await renderTeam(<TeamLoading />)
    assert.ok(container.querySelector('[role="status"][aria-label="Loading"]'))
    let retries = 0
    await renderTeam(
      <TeamError
        retry={() => {
          retries += 1
        }}
      />
    )
    const retry = [...container.querySelectorAll('button')].find(
      (button) => button.textContent === 'Retry'
    )
    assert.ok(retry)
    await act(async () => retry.click())
    assert.equal(retries, 1)
  })

  test('non-root administrators can inspect policy but cannot change or save it', async () => {
    await renderTeam(<PolicyForm policy={policy} writable={false} />)
    for (const input of container.querySelectorAll('input')) {
      assert.equal(input.disabled, true)
    }
    assert.equal(
      [...container.querySelectorAll('button')].some((button) =>
        button.textContent?.includes('Save reward settings')
      ),
      false
    )
    assert.ok(container.textContent?.includes('Only the root administrator'))
  })

  test('payout binding displays masked data without pre-filling sensitive credentials', async () => {
    await renderTeam(<PayoutForm data={self} />)
    assert.ok(container.textContent?.includes('p***@example.test'))
    assert.equal(
      container.querySelector<HTMLInputElement>('#team-account')?.value,
      ''
    )
    assert.equal(
      container.querySelector<HTMLInputElement>('#team-name')?.value,
      ''
    )
  })

  test('withdrawals are unavailable while disabled, unbound, unconfigured or below the minimum', async () => {
    const cases = [
      { ...self, policy: { ...policy, enabled: false } },
      { ...self, payout: { ...self.payout, bound: false } },
      { ...self, payout_ready: false },
      { ...self, wallet: { ...self.wallet, available_cents: 999 } },
    ]
    for (const data of cases) {
      await renderTeam(<WithdrawalForm data={data} />)
      assert.equal(
        container.querySelector<HTMLInputElement>('#team-withdraw-amount')
          ?.disabled,
        true
      )
      assert.equal(
        container.querySelector<HTMLButtonElement>('button[type="submit"]')
          ?.disabled,
        true
      )
    }
    await renderTeam(<WithdrawalForm data={self} />)
    assert.equal(
      container.querySelector<HTMLInputElement>('#team-withdraw-amount')
        ?.disabled,
      false
    )
  })
})
