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
export type PoolGift = {
  amount: string
  currency: string
  valid_seconds: number
}
export type PoolPrize = {
  id?: number
  name: string
  weight: number
  stock: number
  gifts: PoolGift[]
}
export type PoolSpec = {
  name: string
  style: 'wheel' | 'blind_box'
  starts_at: number
  ends_at: number
  capacity: number
  prizes: PoolPrize[]
}
export type PoolRecord = {
  money_revision: string
  id: number
  revision: number
  state: 'scheduled' | 'active' | 'paused' | 'ended'
  editable: boolean
  issued: number
  remaining_capacity: number
  appendable_capacity: number | null
  pool: PoolSpec
}
export type PoolPage = {
  items: PoolRecord[]
  total: number
  page: number
  page_size: number
}
export type PoolAction = 'pause' | 'resume' | 'restock' | 'append' | 'extend'
