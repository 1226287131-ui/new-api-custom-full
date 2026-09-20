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
import { afterEach, beforeEach, describe, test } from 'node:test'

import type { InternalAxiosRequestConfig } from 'axios'
import i18next from 'i18next'

import { api } from '@/lib/api'

import {
  getCommissions,
  getTeamSelf,
  getWithdrawalPayout,
  getWithdrawals,
  requestWithdrawal,
  reviewWithdrawal,
  savePayout,
  saveTeamPolicy,
} from '../api'

const originalAdapter = api.defaults.adapter
let requests: InternalAxiosRequestConfig[] = []
let response: unknown

describe('Team API contracts', () => {
  beforeEach(async () => {
    await i18next.init({ lng: 'en', resources: { en: { translation: {} } } })
    requests = []
    response = {
      success: true,
      data: { items: [], total: 0, page: 1, page_size: 20 },
    }
    api.defaults.adapter = async (config) => {
      requests.push(config)
      return {
        data: response,
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      }
    }
  })
  afterEach(() => {
    api.defaults.adapter = originalAdapter
  })

  test('uses separate personal and administrator list endpoints with page and status filters', async () => {
    await getCommissions(2)
    await getCommissions(3, true)
    await getWithdrawals(4, true, 'pending')
    assert.deepEqual(
      requests.map((request) => request.url),
      [
        '/api/team/commissions',
        '/api/team/admin/commissions',
        '/api/team/admin/withdrawals',
      ]
    )
    assert.deepEqual(
      requests.map((request) => request.params.p),
      [2, 3, 4]
    )
    assert.equal(requests[2].params.status, 'pending')
    assert.equal(requests[2].params.page_size, 20)
  })

  test('carries exact cents and the same request ID in retried withdrawal requests', async () => {
    await requestWithdrawal(1234, 'same-request', 'proof-a')
    await requestWithdrawal(1234, 'same-request', 'proof-b')
    assert.deepEqual(JSON.parse(requests[0].data), {
      amount_cents: 1234,
      request_id: 'same-request',
    })
    assert.equal(requests[0].data, requests[1].data)
    assert.equal(requests[1].headers.get('X-Security-Proof'), 'proof-b')
    assert.equal(requests[0].url, '/api/team/withdrawals')
  })

  test('payout disclosure and review send security proofs and explicit action fields', async () => {
    await savePayout(
      { account: 'pay@example.test', name: 'Li Ming' },
      'payout-proof'
    )
    await getWithdrawalPayout(7, 'read-proof')
    await reviewWithdrawal(
      7,
      { action: 'paid', reference: 'TRANSFER-123', reason: '' },
      'review-proof'
    )
    assert.equal(requests[0].method, 'put')
    assert.equal(requests[0].url, '/api/team/payout')
    assert.equal(requests[1].url, '/api/team/admin/withdrawals/7/payout')
    assert.equal(requests[1].method, 'post')
    assert.equal(requests[1].headers.get('X-Security-Proof'), 'read-proof')
    assert.equal(requests[2].url, '/api/team/admin/withdrawals/7/review')
    assert.deepEqual(JSON.parse(requests[2].data), {
      action: 'paid',
      reference: 'TRANSFER-123',
      reason: '',
    })
    assert.equal(requests[2].headers.get('X-Security-Proof'), 'review-proof')
  })

  test('saves rates as basis points without converting quota or currency locally', async () => {
    const policy = {
      enabled: true,
      mode: 'credit' as const,
      credit_rate_bps: 500,
      cash_rate_bps: 500,
      freeze_hours: 24,
      minimum_withdrawal_cents: 1000,
    }
    await saveTeamPolicy(policy)
    assert.equal(requests[0].url, '/api/team/admin/policy')
    assert.deepEqual(JSON.parse(requests[0].data), policy)
  })

  test('business failures do not become successful team data', async () => {
    response = { success: false, message: 'Access denied' }
    await assert.rejects(getTeamSelf(), {
      message: 'Unable to complete this team request',
    })
    assert.equal(requests[0].skipBusinessError, true)
  })

  test('known server errors use the active locale instead of raw English', async () => {
    await i18next.init({
      lng: 'zh',
      resources: {
        zh: {
          translation: {
            'Insufficient available rewards': '可提现奖励余额不足',
          },
        },
      },
    })
    response = { success: false, message: 'Insufficient available rewards' }
    await assert.rejects(
      requestWithdrawal(1000, 'request-localized', 'proof'),
      { message: '可提现奖励余额不足' }
    )
    await i18next.changeLanguage('en')
  })
})
