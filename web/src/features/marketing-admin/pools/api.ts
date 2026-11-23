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
import { marketingGet, marketingWrite } from '../api'
import type { PoolAction, PoolPage, PoolRecord, PoolSpec } from './types'

export const poolQueryKey = ['marketing-admin', 'pools'] as const
export const getPools = (page: number, pageSize: number, keyword: string) =>
  marketingGet<PoolPage>('/pools', { page, page_size: pageSize, keyword })
export const getPool = (id: number) => marketingGet<PoolRecord>(`/pools/${id}`)
export const savePool = (
  input: {
    operation_key: string
    money_revision: string
    pool: PoolSpec
    revision?: number
  },
  id?: number
) =>
  marketingWrite<PoolRecord>(
    id === undefined ? '/pools' : `/pools/${id}`,
    input,
    id !== undefined
  )
export const mutatePool = (
  id: number,
  action: PoolAction,
  input: {
    operation_key: string
    revision: number
    prize_id?: number
    amount?: number
    ends_at?: number
  }
) => marketingWrite<PoolRecord>(`/pools/${id}/${action}`, input)
