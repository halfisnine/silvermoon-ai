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
import { requireServerSuccess } from '@/lib/server-error-message'

import type {
  CheckinCalendar,
  CheckinResult,
  MarketingPage,
  PublicActivity,
} from './types'

export async function userMarketingGet<T>(
  path: string,
  params: Record<string, string | number> = {}
): Promise<T> {
  const response = await api.get<{
    success: boolean
    message: string
    data: T
  }>(`/api/marketing/user${path}`, { params })
  return requireServerSuccess(response.data).data
}
export const getUserActivities = (page = 1, kind = '', state = '') =>
  userMarketingGet<MarketingPage<PublicActivity>>('/activities', {
    page,
    page_size: 20,
    kind,
    state,
  })
export async function getCalendar(month: string): Promise<CheckinCalendar> {
  const response = await api.get<{
    success: boolean
    message: string
    data: CheckinCalendar
  }>('/api/user/checkin', { params: { month } })
  return requireServerSuccess(response.data).data
}
export async function submitCheckin(
  date: string,
  token?: string
): Promise<CheckinResult> {
  const path = date ? '/api/user/checkin/makeup' : '/api/user/checkin'
  const response = await api.post<{
    success: boolean
    message: string
    data: CheckinResult
  }>(path, date ? { date } : {}, { params: token ? { turnstile: token } : {} })
  return requireServerSuccess(response.data).data
}
