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
import i18next from 'i18next'

import { api } from '@/lib/api'

import type {
  Commission,
  PayoutAccount,
  TeamPage,
  TeamPolicy,
  TeamSelf,
  Withdrawal,
  WithdrawalReview,
} from './types'

interface Response<T> {
  success: boolean
  message?: string
  data: T
}

const teamErrorKeys = new Set([
  'Invalid payout account',
  'Invalid withdrawal request',
  'Invalid withdrawal status',
  'Invalid withdrawal review',
  'Invalid invitation reward settings',
  'Unable to complete this team request',
  'Payout account encryption is not configured',
  'Insufficient available rewards',
  'Withdrawals are currently disabled',
  'This withdrawal status does not allow this action',
  'This withdrawal request has already been submitted with a different amount',
  'Bind an Alipay account first',
  'Invalid withdrawal amount or below the minimum',
])

function unwrap<T>(response: Response<T>): T {
  if (!response.success) {
    const key =
      response.message && teamErrorKeys.has(response.message)
        ? response.message
        : 'Unable to complete this team request'
    throw new Error(i18next.t(key))
  }
  return response.data
}

export async function getTeamSelf(): Promise<TeamSelf> {
  return unwrap(
    (
      await api.get<Response<TeamSelf>>('/api/team/self', {
        skipBusinessError: true,
      })
    ).data
  )
}

export async function getTeamPolicy(): Promise<TeamPolicy> {
  return unwrap(
    (
      await api.get<Response<TeamPolicy>>('/api/team/admin/policy', {
        skipBusinessError: true,
      })
    ).data
  )
}

export async function getCommissions(
  page: number,
  admin = false
): Promise<TeamPage<Commission>> {
  return unwrap(
    (
      await api.get<Response<TeamPage<Commission>>>(
        `/api/team/${admin ? 'admin/' : ''}commissions`,
        {
          params: { p: page, page_size: 20 },
          skipBusinessError: true,
        }
      )
    ).data
  )
}

export async function getWithdrawals(
  page: number,
  admin = false,
  status?: string
): Promise<TeamPage<Withdrawal>> {
  return unwrap(
    (
      await api.get<Response<TeamPage<Withdrawal>>>(
        `/api/team/${admin ? 'admin/' : ''}withdrawals`,
        {
          params: {
            p: page,
            page_size: 20,
            status: status || undefined,
          },
          skipBusinessError: true,
        }
      )
    ).data
  )
}

export async function savePayout(
  body: PayoutAccount,
  proof: string
): Promise<void> {
  unwrap(
    (
      await api.put<Response<void>>('/api/team/payout', body, {
        skipBusinessError: true,
        singleUseAuthorization: true,
        headers: { 'X-Security-Proof': proof },
      })
    ).data
  )
}

export async function requestWithdrawal(
  amountCents: number,
  requestID: string,
  proof: string
): Promise<void> {
  unwrap(
    (
      await api.post<Response<void>>(
        '/api/team/withdrawals',
        { amount_cents: amountCents, request_id: requestID },
        {
          skipBusinessError: true,
          singleUseAuthorization: true,
          headers: { 'X-Security-Proof': proof },
        }
      )
    ).data
  )
}

export async function saveTeamPolicy(
  body: TeamPolicy,
  proof?: string
): Promise<void> {
  unwrap(
    (
      await api.put<Response<void>>('/api/team/admin/policy', body, {
        skipBusinessError: true,
        headers: { 'X-Security-Proof': proof },
      })
    ).data
  )
}

export async function getWithdrawalPayout(
  id: number,
  proof: string
): Promise<PayoutAccount> {
  return unwrap(
    (
      await api.post<Response<PayoutAccount>>(
        `/api/team/admin/withdrawals/${id}/payout`,
        {},
        {
          skipBusinessError: true,
          singleUseAuthorization: true,
          headers: { 'X-Security-Proof': proof },
        }
      )
    ).data
  )
}

export async function reviewWithdrawal(
  id: number,
  body: WithdrawalReview,
  proof: string
): Promise<void> {
  unwrap(
    (
      await api.post<Response<void>>(
        `/api/team/admin/withdrawals/${id}/review`,
        body,
        {
          skipBusinessError: true,
          singleUseAuthorization: true,
          headers: { 'X-Security-Proof': proof },
        }
      )
    ).data
  )
}
