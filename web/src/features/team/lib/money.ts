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
import { z } from 'zod'

import { toIntlLocale } from '@/i18n/languages'

// Parse displayed decimal units without accepting exponents or sub-cent rounding.
export function parseHundredths(value: string): number | null {
  if (!/^\d+(?:\.\d{1,2})?$/.test(value)) return null
  const [whole, fraction = ''] = value.split('.')
  const result = Number(whole) * 100 + Number(fraction.padEnd(2, '0'))
  return Number.isSafeInteger(result) ? result : null
}

export function formatCNY(cents: number, locale: string): string {
  return new Intl.NumberFormat(toIntlLocale(locale), {
    style: 'currency',
    currency: 'CNY',
  }).format(cents / 100)
}

export const payoutSchema = z.object({
  account: z
    .string()
    .trim()
    .min(3)
    .max(128)
    .regex(/^[^\p{Cc}]+$/u),
  name: z
    .string()
    .trim()
    .min(2)
    .max(128)
    .regex(/^[^\p{Cc}]+$/u),
})

export const policySchema = z.object({
  enabled: z.boolean(),
  mode: z.enum(['credit', 'cash']),
  credit_rate: z.string().refine((value) => {
    const parsed = parseHundredths(value)
    return parsed !== null && parsed <= 10000
  }),
  cash_rate: z.string().refine((value) => {
    const parsed = parseHundredths(value)
    return parsed !== null && parsed <= 10000
  }),
  freeze_hours: z
    .string()
    .regex(/^\d+$/)
    .refine((value) => Number(value) <= 8760),
  minimum: z.string().refine((value) => {
    const parsed = parseHundredths(value)
    return parsed !== null && parsed > 0 && parsed <= 100000000000
  }),
})

export function withdrawalSchema(minimum: number, available: number) {
  return z.object({
    amount: z.string().refine((value) => {
      const parsed = parseHundredths(value)
      return (
        parsed !== null &&
        parsed >= minimum &&
        parsed > 0 &&
        parsed <= available
      )
    }),
  })
}
