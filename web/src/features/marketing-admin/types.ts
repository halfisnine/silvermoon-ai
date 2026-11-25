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
export type MarketingReward = {
  state?: string
  credited_at?: number
  expires_at?: number
  kind: 'gift' | 'chance'
  calculation?: 'fixed' | 'paid_ratio' | 'cash_ratio'
  amount?: string
  currency?: string
  ratio_percent?: string
  valid_seconds?: number
  pool_id?: number
  pool_name?: string
  count?: number
  chance_mode?: 'direct' | 'probability'
  probability_percent?: string
}
export type MarketingCheckin = {
  condition: 'none' | 'today_paid' | 'previous_day_paid'
  threshold_cny: string
  cycle_days: number
  makeup_days: number
  makeup_limit: number
  makeup_daily: boolean
  milestones: { days: number; rewards: MarketingReward[] }[]
}
export type MarketingActivity = {
  name: string
  kind: 'registration' | 'checkin' | 'recharge' | 'invitation'
  condition: string
  frequency: 'once' | 'every'
  published: boolean
  starts_at: number
  ends_at: number
  threshold_cny: string
  exclusive_group: string
  priority: number
  rewards: MarketingReward[]
  checkin: MarketingCheckin | null
}
export type ActivityDetail = {
  archived: boolean
  deletable: boolean
  id: number
  revision: number
  state: string
  money_revision: string
  enabled_at: number
  editable: boolean
  activity: MarketingActivity
}
export type MarketingPage<T> = {
  items: T[]
  total: number
  page: number
  page_size: number
}
export type PoolOption = {
  id: number
  state: string
  pool: { name: string }
  remaining_capacity: number
}
