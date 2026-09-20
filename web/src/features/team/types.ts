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
export interface TeamPolicy {
  enabled: boolean
  mode: 'credit' | 'cash'
  credit_rate_bps: number
  cash_rate_bps: number
  freeze_hours: number
  minimum_withdrawal_cents: number
}

export interface TeamSelf {
  policy: TeamPolicy
  wallet: { available_cents: number; frozen_cents: number; paid_cents: number }
  payout: {
    bound: boolean
    account_masked: string
    name_masked: string
    updated_at: number
  }
  payout_ready: boolean
  summary: {
    referred_users: number
    order_count: number
    pending_credit_quota: number
    settled_credit_quota: number
    pending_cash_cents: number
    settled_cash_cents: number
  }
}

export interface Commission {
  id: number
  user_id: number
  referrer_id: number
  trade_no: string
  mode: 'credit' | 'cash'
  rate_bps: number
  paid_cents: number
  credit_quota: number
  cash_cents: number
  quota_per_unit: string
  status: 'pending' | 'settled'
  created_at: number
  ready_at: number
  settled_at: number
}

export interface Withdrawal {
  id: number
  user_id: number
  request_id: string
  amount_cents: number
  status: 'pending' | 'approved' | 'paid' | 'rejected'
  account_masked: string
  name_masked: string
  created_at: number
  reviewed_at: number
  paid_at: number
  payment_reference: string
  rejection_reason: string
}

export interface TeamPage<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}

export interface PayoutAccount {
  account: string
  name: string
}
export interface WithdrawalReview {
  action: 'approve' | 'paid' | 'reject'
  reference: string
  reason: string
}
