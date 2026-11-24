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
import { marketingGet } from '@/features/marketing-admin/api'
import type { MarketingPage } from '@/features/marketing-admin/types'

import { userMarketingGet } from './api'

export type AccountingMoney = { amount: string; currency: string }
export type WalletBalance = { paid: AccountingMoney; gift: AccountingMoney }
export type MonthlyPaidConsumption = {
  month: string
  amounts: AccountingMoney[]
  conversion_unknown_count: number
}
export type AccountingGiftSource = {
  gift_id: number
  source_name: string
  source_name_key?: string
  expires_at: number
  reward_id?: number
  reward_kind?: string
  money: AccountingMoney | null
}
export type RequestAccounting = {
  id: number
  user_id: number
  request_id: string
  model_name: string
  realtime: boolean
  realtime_closed: boolean
  actual: AccountingMoney | null
  state: string
  cost_known: boolean
  final_observed: boolean
  conversion_known: boolean
  total: AccountingMoney | null
  paid: AccountingMoney | null
  gift: AccountingMoney | null
  unsettled: AccountingMoney | null
  pending_return?: AccountingMoney | null
  reserved: AccountingMoney | null
  gift_sources: AccountingGiftSource[]
  qualified_at: number
}
export const getWalletBalance = () => userMarketingGet<WalletBalance>('/wallet')
export const getMonthlyPaidConsumption = (month: string) =>
  userMarketingGet<MonthlyPaidConsumption>('/consumption', { month })
export const getAccountingRequests = (
  params: Record<string, string | number>
) => marketingGet<MarketingPage<RequestAccounting>>('/accounting', params)

export type AccountingPayment = {
  id: number
  user_id: number
  kind: string
  paid_cny: string
  credited: AccountingMoney | null
  posted_at: number
  order_id?: number
  order_reference?: string
  provider?: string
  redemption_id?: number
  redemption_name?: string
  sales_channel?: string
  sales_batch?: string
  fact_id?: number
}
export type AccountingCashRefund = {
  id: number
  user_id: number
  recorded_at: number
  exact_refund_cny: string
  refunded_cny: string
  rounding_difference_cny: string
  cleared_paid: AccountingMoney | null
  cleared_gift: AccountingMoney | null
}
