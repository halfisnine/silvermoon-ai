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
import type {
  MarketingReward,
  MarketingCheckin,
  MarketingPage,
} from '@/features/marketing-admin/types'

export type { MarketingReward, MarketingPage }
export type ActivityKind =
  | 'registration'
  | 'checkin'
  | 'recharge'
  | 'invitation'
export type PoolSummary = {
  id: number
  name: string
  state: string
  starts_at: number
  ends_at: number
  capacity: number
  remaining_capacity: number
}
export type PublicActivity = {
  id: number
  state: string
  server_now: number
  activity: {
    name: string
    kind: ActivityKind
    condition: string
    frequency: string
    starts_at: number
    ends_at: number
    threshold_cny?: string
    rewards: MarketingReward[]
    checkin: MarketingCheckin | null
  }
  pools: PoolSummary[]
}
export type RewardRecord = {
  id: number
  kind: 'activity' | 'lottery'
  source_name: string
  state: string
  occurred_at: number
  credited_at: number
  rewards: MarketingReward[]
  source: Record<string, string | number | boolean>
}
export type Gift = {
  id: number
  source_name: string
  source_name_key?: string
  source_kind: string
  currency: string
  granted_amount: string
  consumed_amount: string
  remaining_amount: string
  expired_amount: string
  cleared_amount: string
  credited_at: number
  expires_at: number
  state: string
  reward_id?: number
  reward_kind?: 'activity' | 'lottery'
}
export type Invitation = {
  invitation_code: string
  invitation_path: string
  friend_count: number
  friends: MarketingPage<{
    username: string
    joined_at: number
    rewards: { granted: number; pending: number; other: number }
  }>
}
export type CheckinActivity = {
  id: number
  name: string
  state: string
  starts_at: number
  ends_at: number
  paused: boolean
  condition: string
  required_payment_cny: string
  cycle_days: number
  makeup_days: number
  makeup_limit: number
  makeup_daily: boolean
  makeup_used: number
  makeup_remaining: number
  makeup_dates: string[]
  streak: number
  today_eligibility: string
  daily_rewards: MarketingReward[]
  milestones: { days: number; rewards: MarketingReward[] }[]
  records: { date: string; makeup: boolean }[]
}
export type CheckinCalendar = {
  month: string
  today: string
  server_now: number
  activities: CheckinActivity[]
  can_checkin: boolean
  checked_in_today: boolean
}
export type CheckinResult = {
  date: string
  makeup: boolean
  already_recorded: boolean
  rewards_pending: boolean
}
