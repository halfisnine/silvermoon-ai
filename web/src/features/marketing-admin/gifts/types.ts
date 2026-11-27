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
import type { Gift } from '@/features/marketing/types'

export type GiftUser = { id: number; username: string; display_name: string }
export type AdminGift = Gift & {
  user_id: number
  username: string
  batch_id?: number
}
export type GiftRecipient = {
  id: number
  user_id: number
  username: string
  state: string
  error_key?: string
  updated_at: number
  last_actor_id: number
  gift?: Gift
}
export type GiftBatch = {
  id: number
  actor_id: number
  actor_name: string
  reason: string
  amount: string
  total_amount: string
  currency: string
  valid_seconds: number
  created_at: number
  total: number
  credited: number
  failed: number
  pending: number
  recipients?: GiftRecipient[]
}
export type GiftBatchInput = {
  operation_key: string
  money_revision: string
  user_ids: number[]
  amount: string
  currency: string
  valid_seconds: number
  reason: string
}
