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
import { api } from '@/lib/api'
import { loadAllOptions } from '@/lib/paginated-options'
import { requireServerSuccess } from '@/lib/server-error-message'

import type { LotteryDraw, LotteryPageData, LotteryPool } from './types'

const base = '/api/marketing/user'
export async function getLotteryPools(): Promise<LotteryPool[]> {
  return loadAllOptions(async (page) => {
    const response = await api.get(`${base}/pools`, {
      params: { page, page_size: 100 },
    })
    return requireServerSuccess<{ data: LotteryPageData }>(response.data).data
  })
}
export async function getLotteryPool(id: number): Promise<LotteryPool> {
  const response = await api.get(`${base}/pools/${id}`)
  return requireServerSuccess(response.data).data
}
export async function getLotteryDraw(key: string): Promise<LotteryDraw | null> {
  try {
    const response = await api.get(
      `${base}/draws/by-request/${encodeURIComponent(key)}`
    )
    return requireServerSuccess(response.data).data
  } catch (error) {
    if (
      typeof error === 'object' &&
      error !== null &&
      'response' in error &&
      (error.response as { status?: number })?.status === 404
    ) {
      return null
    }
    throw error
  }
}
export async function submitLotteryDraw(
  pool: number,
  key: string
): Promise<LotteryDraw> {
  const response = await api.post(`${base}/pools/${pool}/draw`, {
    operation_key: key,
  })
  return requireServerSuccess(response.data).data
}
