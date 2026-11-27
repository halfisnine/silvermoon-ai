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
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { getBatchPricing } from '../api'

afterEach(() => vi.restoreAllMocks())

it('reads every long model name using bounded URLs without losing individual versions', async () => {
  const names = [
    'plain',
    '模型'.repeat(60),
    'other-model',
    '別名'.repeat(60),
    '扩展'.repeat(60),
  ]
  const get = vi.spyOn(api, 'get').mockImplementation(async (_url, options) => {
    const params = options?.params as URLSearchParams
    expect(params.toString().length).toBeLessThanOrEqual(3000)
    return {
      data: {
        success: true,
        data: {
          entries: params.getAll('model').map((name) => ({
            model_name: name,
            version: `v:${name}`,
            configured: {},
            effective: {},
          })),
          options: {},
          empty_version: 'empty',
        },
      },
    }
  })
  const result = await getBatchPricing(names)
  expect(get.mock.calls.length).toBeGreaterThan(1)
  expect(
    result.entries.map((entry) => [entry.model_name, entry.version])
  ).toEqual(names.map((name) => [name, `v:${name}`]))
  expect(result.empty_version).toBe('empty')
})
