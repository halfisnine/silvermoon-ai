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
export type LotteryGift = {
  amount: string
  currency: string
  valid_seconds: number
  state?: string
  credited_at?: number
  expires_at?: number
}
export type LotteryPrize = {
  id: number
  name: string
  available: boolean
  state: 'available' | 'sold_out' | 'unavailable'
  no_prize: boolean
  gifts: LotteryGift[]
}
export type LotteryPool = {
  id: number
  name: string
  state: 'active' | 'scheduled' | 'ended' | 'paused'
  starts_at: number
  ends_at: number
  server_now: number
  style: 'wheel' | 'blind_box'
  available_chances: number
  can_draw: boolean
  chances: {
    id: number
    remaining: number
    expires_at: number
    state: 'ready' | 'expired' | 'cleared' | 'used'
    source_name: string
  }[]
  prizes: LotteryPrize[]
}
export type LotteryDraw = {
  id: number
  pool_id: number
  pool_name: string
  prize_id: number
  prize_name: string
  no_prize: boolean
  state: 'pending' | 'completed' | 'cleared'
  drawn_at: number
  rewards: LotteryGift[]
}
export type LotteryPageData = {
  items: LotteryPool[]
  total: number
  page: number
  page_size: number
}
