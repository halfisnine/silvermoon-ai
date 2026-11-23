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
  ActivityDetail,
  MarketingActivity,
  MarketingPage,
  PoolOption,
} from './types'

const base = '/api/marketing/admin'
type Envelope<T> = { success: boolean; message: string; data: T }
export async function marketingGet<T>(
  path: string,
  params: Record<string, string | number> = {}
): Promise<T> {
  const response = await api.get<Envelope<T>>(`${base}${path}`, { params })
  return requireServerSuccess(response.data).data
}
export async function marketingWrite<T>(
  path: string,
  body: unknown,
  update = false
): Promise<T> {
  const response = update
    ? await api.put<Envelope<T>>(`${base}${path}`, body)
    : await api.post<Envelope<T>>(`${base}${path}`, body)
  return requireServerSuccess(response.data).data
}
export const getMarketingMetadata = () =>
  marketingGet<{ currency: string; money_revision: string }>('/metadata')
export const getActivities = (
  page: number,
  pageSize: number,
  keyword: string,
  kind: string
) =>
  marketingGet<MarketingPage<ActivityDetail>>('/activities', {
    page,
    page_size: pageSize,
    keyword,
    kind,
  })
export async function getActivity(id: number): Promise<ActivityDetail> {
  const detail = await marketingGet<ActivityDetail>(`/activities/${id}`)
  detail.activity.threshold_cny ??= ''
  if (detail.activity.checkin) detail.activity.checkin.threshold_cny ??= ''
  return detail
}
export const getPoolOptions = (page: number, keyword: string) =>
  marketingGet<MarketingPage<PoolOption>>('/pools', {
    page,
    page_size: 20,
    keyword,
  })
export const saveActivity = (
  activity: MarketingActivity,
  operationKey: string,
  moneyRevision: string,
  original?: ActivityDetail
) =>
  marketingWrite<ActivityDetail>(
    original ? `/activities/${original.id}` : '/activities',
    {
      operation_key: operationKey,
      money_revision: moneyRevision,
      revision: original?.revision,
      activity,
    },
    !!original
  )
