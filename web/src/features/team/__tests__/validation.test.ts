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

import { describe, test } from 'vitest'

import { teamDate } from '../lib/date'
import {
  formatCNY,
  parseHundredths,
  payoutSchema,
  policySchema,
  withdrawalSchema,
} from '../lib/money'

describe('Team financial input validation', () => {
  test('renders amounts and dates with the application Chinese language codes', () => {
    for (const [language, locale] of [
      ['zhCN', 'zh-CN'],
      ['zhTW', 'zh-TW'],
    ]) {
      assert.equal(
        formatCNY(12345, language),
        new Intl.NumberFormat(locale, {
          style: 'currency',
          currency: 'CNY',
        }).format(123.45)
      )
      assert.equal(
        teamDate(1700000000, language),
        new Date(1700000000000).toLocaleString(locale)
      )
    }
  })

  test('converts exact decimal currency and rates to integer hundredths', () => {
    for (const [value, expected] of [
      ['0', 0],
      ['5', 500],
      ['5.01', 501],
      ['0.1', 10],
      ['1000000000', 100000000000],
    ] as const) {
      assert.equal(parseHundredths(value), expected)
    }
  })

  test('rejects signs, exponents, fractional cents and unsafe integers', () => {
    for (const value of [
      '',
      '-1',
      '+1',
      '1e3',
      ' 1',
      '1 ',
      '0.001',
      'NaN',
      'Infinity',
      '90071992547410',
    ]) {
      assert.equal(parseHundredths(value), null, value)
    }
  })

  test('bounds percentages, withdrawal minimum and freeze period before saving policy', () => {
    const policy = {
      enabled: true,
      mode: 'credit',
      credit_rate: '5',
      cash_rate: '5',
      freeze_hours: '24',
      minimum: '10',
    }
    assert.equal(policySchema.safeParse(policy).success, true)
    for (const override of [
      { credit_rate: '100.01' },
      { cash_rate: '-1' },
      { freeze_hours: '8761' },
      { freeze_hours: '0.5' },
      { minimum: '0' },
      { minimum: '1000000000.01' },
    ]) {
      assert.equal(
        policySchema.safeParse({ ...policy, ...override }).success,
        false
      )
    }
    assert.equal(
      policySchema.safeParse({
        ...policy,
        credit_rate: '100',
        cash_rate: '0',
        freeze_hours: '8760',
        minimum: '1000000000',
      }).success,
      true
    )
  })

  test('withdrawals must meet the minimum without exceeding available cash', () => {
    const schema = withdrawalSchema(1000, 2500)
    assert.equal(schema.safeParse({ amount: '10' }).success, true)
    assert.equal(schema.safeParse({ amount: '25' }).success, true)
    for (const amount of ['9.99', '25.01', '-1', '10.001']) {
      assert.equal(schema.safeParse({ amount }).success, false)
    }
  })

  test('normalizes payout text but rejects empty, control-character and oversized values', () => {
    assert.deepEqual(
      payoutSchema.parse({ account: ' pay@example.test ', name: ' Li Ming ' }),
      { account: 'pay@example.test', name: 'Li Ming' }
    )
    for (const override of [
      { account: '12' },
      { name: ' ' },
      { account: 'a\nb' },
      { name: 'Li\tMing' },
      { account: 'x'.repeat(129) },
    ]) {
      assert.equal(
        payoutSchema.safeParse({
          account: 'pay@example.test',
          name: 'Li Ming',
          ...override,
        }).success,
        false
      )
    }
  })
})
