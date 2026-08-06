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
import { describe, test } from 'node:test'

import { normalizeApiKey, normalizeGetApiKeysResponse } from '../types'

describe('API key response normalization', () => {
  test('normalizes nullable legacy token fields without throwing', () => {
    const apiKey = normalizeApiKey({
      id: '7',
      name: 'legacy token',
      key: 'masked-key',
      status: 1,
      remain_quota: '100',
      used_quota: 5,
      unlimited_quota: 0,
      expired_time: -1,
      created_time: 10,
      accessed_time: 20,
      group: null,
      auto_groups: null,
      cross_group_retry: null,
      model_limits_enabled: null,
      model_limits: null,
      allow_ips: null,
    })

    assert.ok(apiKey)
    assert.equal(apiKey.id, 7)
    assert.equal(apiKey.group, '')
    assert.equal(apiKey.model_limits_enabled, false)
    assert.equal(apiKey.allow_ips, '')
  })

  test('drops an invalid row while keeping the rest of the key page usable', () => {
    const response = normalizeGetApiKeysResponse({
      success: true,
      data: {
        items: [
          {
            id: 1,
            name: 'valid',
            key: 'key',
            status: 1,
            remain_quota: 0,
            used_quota: 0,
            unlimited_quota: true,
            expired_time: -1,
            created_time: 1,
            accessed_time: 0,
            group: 'default',
            model_limits_enabled: false,
            model_limits: '',
            allow_ips: '',
          },
          { id: 'not-a-number' },
        ],
        total: '2',
        page: '1',
        page_size: '20',
      },
    })

    assert.equal(response.success, true)
    assert.equal(response.data?.items.length, 1)
    assert.equal(response.data?.items[0]?.name, 'valid')
    assert.equal(response.data?.total, 2)
  })
})
