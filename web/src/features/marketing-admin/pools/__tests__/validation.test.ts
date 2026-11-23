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
import { describe, expect, test } from 'vitest'

import {
  poolFormDefaults,
  poolFormPayload,
  poolFormSchema,
} from '../lib/schema'
import type { PoolSpec } from '../types'

const future = Math.floor(Date.now() / 1000) + 86400
const pool: PoolSpec = {
  name: 'Combination',
  style: 'wheel',
  starts_at: future,
  ends_at: future + 86400,
  capacity: 1,
  prizes: [
    {
      id: 72,
      name: 'Two gifts',
      stock: 1,
      weight: 1,
      gifts: [
        { amount: '0.000001', currency: 'CNY', valid_seconds: 7200 },
        { amount: '3', currency: 'CNY', valid_seconds: 0 },
      ],
    },
  ],
}

describe('Prize pool configuration', () => {
  test('a combination consumes one stock unit regardless of its number of gifts', () => {
    const values = poolFormDefaults(pool)
    expect(poolFormSchema.safeParse(values).success).toBe(true)
    values.capacity = 2
    expect(poolFormSchema.safeParse(values).success).toBe(false)
  })
  test('an empty prize does not increase gift supply and requires explicit permission', () => {
    const values = poolFormDefaults(pool)
    values.prizes.push({ name: 'No prize', weight: 10, stock: 100, gifts: [] })
    expect(poolFormSchema.safeParse(values).success).toBe(false)
    values.allow_empty = true
    expect(poolFormSchema.safeParse(values).success).toBe(true)
    values.capacity = 2
    expect(poolFormSchema.safeParse(values).success).toBe(false)
  })
  test('unlimited prize stock still requires a finite positive chance limit', () => {
    const values = poolFormDefaults(pool)
    values.prizes[0].stock = -1
    values.capacity = 10000
    expect(poolFormSchema.safeParse(values).success).toBe(true)
    values.capacity = Infinity
    expect(poolFormSchema.safeParse(values).success).toBe(false)
    values.capacity = 0
    expect(poolFormSchema.safeParse(values).success).toBe(false)
  })
  test('display amounts and duration survive a save round trip without quota conversion', () => {
    const values = poolFormDefaults(pool)
    expect(poolFormPayload(values, 'CNY')).toEqual({
      ...pool,
      prizes: [
        { name: 'Two gifts', stock: 1, weight: 1, gifts: pool.prizes[0].gifts },
      ],
    })
    values.prizes[0].gifts[0].amount = '0'
    expect(poolFormSchema.safeParse(values).success).toBe(false)
    values.prizes[0].gifts[0].amount = '0.000001'
    expect(poolFormSchema.safeParse(values).success).toBe(true)
  })
  test('copying requires a new name and dates and does not retain internal prize identities', () => {
    const copy = poolFormDefaults(pool, true)
    expect(copy.name).toBe('')
    expect(copy.starts_at).toBe(0)
    expect(copy.ends_at).toBe(0)
    expect(copy.prizes[0].stock).toBe(1)
    expect(copy.prizes[0]).not.toHaveProperty('id')
    expect(poolFormSchema.safeParse(copy).success).toBe(false)
  })
  test('zero weight prizes cannot provide guaranteed capacity and reversed dates are rejected', () => {
    const values = poolFormDefaults(pool)
    values.prizes[0].weight = 0
    expect(poolFormSchema.safeParse(values).success).toBe(false)
    values.prizes[0].weight = 1
    values.ends_at = values.starts_at
    expect(poolFormSchema.safeParse(values).success).toBe(false)
  })
})
