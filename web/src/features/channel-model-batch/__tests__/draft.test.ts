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
import { describe, expect, it } from 'vitest'

import type { ModelPricingConfig } from '@/features/model-pricing/api'
import { pricingOptions } from '@/features/model-pricing/pricing'

import type { BatchSnapshot } from '../api'
import {
  buildChanges,
  createDrafts,
  draftEntry,
  multiplyPrice,
  renameDraft,
  validateDrafts,
} from '../draft'
import { quickPrice, quickPriceValues } from '../quick-pricing'

const snapshot: BatchSnapshot = {
  channel: {
    id: 7,
    name: 'Example',
    type: 1,
    models: 'a,b',
    model_mapping: '{"hidden":"remote"}',
  },
  rows: [
    { source_name: 'a', upstream_name: 'up-a', metadata: null },
    { source_name: 'b', upstream_name: 'up-b', metadata: null },
  ],
  metadata: [],
  vendors: [],
  channels: [],
  version: 'snapshot-v1',
}
const prices: ModelPricingConfig = {
  entries: [],
  options: pricingOptions({}),
  empty_version: 'empty-v1',
}

describe('channel model drafts', () => {
  it.each(['constructor', '__proto__'])(
    'keeps price versions for the literal model name %s',
    (name) => {
      const row = {
        ...createDrafts(snapshot)[0],
        name,
        pricing: { ModelPrice: 0.01 },
        pricingVersion: 'literal-v1',
      }
      const renamed = renameDraft(row, 'alias', snapshot, prices)
      expect(buildChanges([renamed], prices)[0].expected_pricing_version).toBe(
        'empty-v1'
      )
      const restored = renameDraft(renamed, name, snapshot, prices)
      expect(buildChanges([restored], prices)[0].expected_pricing_version).toBe(
        'literal-v1'
      )
    }
  )
  it('retains advanced drafts and captured versions when renaming away and back', () => {
    const pricing = {
      'billing_setting.billing_mode': 'tiered_expr',
      'billing_setting.billing_expr':
        'len < 100 ? tier("short", p * 1) : tier("long", p * 2)',
      'billing_setting.plugin_billing_expr': {
        'provider::a': 'tier("base", u("seconds") * 0.1)',
      },
    }
    const row = {
      ...createDrafts(snapshot)[0],
      pricing,
      pricingVersion: 'a-v1',
      vendorId: 9,
      vendorTouched: true,
    }
    const config = {
      ...prices,
      entries: [
        {
          model_name: 'alias',
          version: 'alias-v8',
          configured: {},
          effective: {},
        },
      ],
    }
    const renamed = renameDraft(row, 'alias', snapshot, config)
    expect(renamed.pricing).toEqual(pricing)
    expect(renamed.vendorId).toBe(9)
    expect(buildChanges([renamed], config)[0].expected_pricing_version).toBe(
      'alias-v8'
    )
    const refreshed = {
      ...config,
      entries: [
        { model_name: 'a', version: 'a-v2', configured: {}, effective: {} },
        {
          model_name: 'alias',
          version: 'alias-v9',
          configured: {},
          effective: {},
        },
      ],
    }
    const restored = renameDraft(renamed, 'a', snapshot, refreshed)
    expect(
      buildChanges([restored], refreshed)[0].expected_pricing_version
    ).toBe('a-v1')
    const again = renameDraft(restored, 'alias', snapshot, refreshed)
    expect(buildChanges([again], refreshed)[0].expected_pricing_version).toBe(
      'alias-v8'
    )
  })
  it('resolves untouched vendor and pricing from the destination name', () => {
    const metadata = {
      id: 1,
      model_name: 'alias',
      vendor_id: 9,
      name_rule: 0,
      status: 1,
      sync_official: 0,
      created_time: 0,
      updated_time: 0,
    }
    const entry = {
      model_name: 'alias',
      version: 'alias-v1',
      configured: { ModelPrice: 0.125 },
      effective: { ModelPrice: 0.125 },
    }
    const config = { ...prices, entries: [entry] }
    const renamed = renameDraft(
      createDrafts(snapshot)[0],
      'alias',
      { ...snapshot, metadata: [metadata] },
      config
    )
    expect(renamed.vendorId).toBe(9)
    expect(draftEntry(renamed, config)).toEqual(entry)
    expect(buildChanges([renamed], config)[0]).not.toHaveProperty('pricing')
  })
  it('keeps original pricing untouched when only changing a customer name', () => {
    const rows = createDrafts(snapshot)
    rows[0].name = 'alias'
    expect(buildChanges(rows, prices)).toEqual([
      { source_name: 'a', model_name: 'alias', vendor_id: 0 },
    ])
  })
  it('preserves the original pricing version after another query refreshes', () => {
    const row = {
      ...createDrafts(snapshot)[0],
      pricing: { ModelPrice: 0.025 },
      pricingVersion: 'old-v1',
    }
    const refreshed = {
      ...prices,
      entries: [
        { model_name: 'a', version: 'new-v2', configured: {}, effective: {} },
      ],
    }
    expect(buildChanges([row], refreshed)[0]).toMatchObject({
      pricing: { ModelPrice: 0.025 },
      expected_pricing_version: 'old-v1',
    })
  })
  it.each(['', 'bad,name', 'bad name', 'a'.repeat(129), 'b', 'hidden'])(
    'rejects invalid or conflicting customer name %j',
    (name) => {
      const rows = createDrafts(snapshot)
      rows[0].name = name
      expect(validateDrafts(rows, snapshot)).toBeTruthy()
    }
  )
  it('accepts decimal and explicit zero prices without changing quantity ratios', () => {
    expect(
      multiplyPrice(
        { ModelRatio: 0.125, CompletionRatio: 4, CacheRatio: 0 },
        1.25
      )
    ).toEqual({ ModelRatio: 0.15625, CompletionRatio: 4, CacheRatio: 0 })
    expect(multiplyPrice({ ModelPrice: 0 }, 1.25)).toEqual({ ModelPrice: 0 })
    expect(multiplyPrice({ ModelPrice: 0.025 }, 2)).toEqual({
      ModelPrice: 0.05,
    })
  })
  it('adjusts tier prices while preserving thresholds and request multipliers', () => {
    const values = {
      'billing_setting.billing_mode': 'tiered_expr',
      'billing_setting.billing_expr':
        'len <= 128000 ? tier("short", p * 0.125 + c * 0) : tier("long", p * 1 + c * 2)',
    }
    const changed = multiplyPrice(values, 2)
    const expression = String(changed['billing_setting.billing_expr'])
    expect(expression).toContain('128000')
    expect(expression).toContain('p * 0.25')
    expect(expression).toContain('c * 0')
    expect(values['billing_setting.billing_expr']).toContain('p * 0.125')
  })
  it.each([0, -1, Infinity, Number.NaN])(
    'rejects invalid price factor %s',
    (factor) => {
      expect(() => multiplyPrice({ ModelPrice: 1 }, factor)).toThrow()
    }
  )
  it('blocks missing prices and custom expressions instead of inventing amounts', () => {
    expect(() => multiplyPrice({}, 2)).toThrow()
    expect(() =>
      multiplyPrice(
        {
          'billing_setting.billing_mode': 'tiered_expr',
          'billing_setting.billing_expr': 'max(p, 3)',
        },
        2
      )
    ).toThrow()
  })
  it('uses the server preview to display fallback amounts without persisting them', () => {
    const row = {
      ...createDrafts(snapshot)[0],
      pricing: { ModelRatio: 0.25 },
      previewPricing: { ModelRatio: 0.25, CompletionRatio: 2 },
    }
    expect(draftEntry(row, prices).effective).toEqual({
      ModelRatio: 0.25,
      CompletionRatio: 2,
    })
    expect(buildChanges([row], prices)[0].pricing).toEqual({ ModelRatio: 0.25 })
  })
})

// Quick fields preserve billing units, precision and unrelated pricing lanes.

it('keeps decimal request prices inline and rejects incomplete or negative input', () => {
  const entry = {
    model_name: 'quick',
    version: 'v',
    configured: {},
    effective: {
      ModelPrice: 0.025,
      'billing_setting.billing_mode': 'ratio',
      'billing_setting.billing_expr':
        '(tier("old", fixed(9))) * (param("fast") == true ? 2 : 1)',
    },
  }
  expect(quickPrice(entry, 250000)).toEqual({
    mode: 'request',
    input: '0.025',
    output: '',
  })
  expect(
    quickPriceValues(
      { mode: 'request', input: '0.000125', output: '' },
      entry,
      250000
    )
  ).toMatchObject({
    'billing_setting.billing_mode': 'tiered_expr',
    'billing_setting.billing_expr': 'tier("request", fixed(0.000125))',
  })
  for (const input of ['', '-', '-0.1', 'NaN', 'Infinity']) {
    expect(() =>
      quickPriceValues({ mode: 'request', input, output: '' }, entry, 250000)
    ).toThrow()
  }
})

it('displays legacy quota units but requires conversion before editing token prices', () => {
  const entry = {
    model_name: 'quick',
    version: 'v',
    configured: { CacheRatio: 0.1 },
    effective: { ModelRatio: 0.5, CompletionRatio: 2, CacheRatio: 0.1 },
  }
  expect(quickPrice(entry, 250000)).toEqual({
    mode: 'token',
    input: '2',
    output: '4',
    cacheRead: '0.2',
    cacheWrite: null,
    longCacheWrite: false,
  })
  expect(() =>
    quickPriceValues(
      { mode: 'token', input: '0.125', output: '0.375' },
      entry,
      250000
    )
  ).toThrow()
})

it('switches a legacy request price to a token expression without activating dormant ratio lanes', () => {
  const configured = {
    ModelPrice: 0.025,
    ModelRatio: 1,
    CacheRatio: 0.1,
    AudioRatio: 2,
  }
  const changed = quickPriceValues(
    { mode: 'token', input: '2', output: '10' },
    { model_name: 'quick', version: 'v', configured, effective: configured },
    500000
  )
  expect(changed).toMatchObject({
    ...configured,
    'billing_setting.billing_mode': 'tiered_expr',
    'billing_setting.billing_expr': 'tier("base", p * 2 + c * 10)',
  })
})

it('retains expression cache leaves and request rules while editing only input/output', () => {
  const entry = {
    model_name: 'quick',
    version: 'v',
    configured: {},
    effective: {
      'billing_setting.billing_mode': 'tiered_expr',
      'billing_setting.billing_expr':
        '(tier("base", p * 1 + c * 2 + cr * 0.1)) * (param("fast") == true ? 2 : 1)',
    },
  }
  const result = quickPriceValues(
    { mode: 'token', input: '0.125', output: '0.375' },
    entry,
    500000
  )
  expect(result['billing_setting.billing_expr']).toContain('cr * 0.1')
  expect(result['billing_setting.billing_expr']).toContain('0.125')
  expect(result['billing_setting.billing_expr']).toContain('param("fast")')
  expect(
    quickPrice(
      {
        ...entry,
        effective: {
          'billing_setting.billing_mode': 'tiered_expr',
          'billing_setting.billing_expr':
            'len < 100 ? tier("short", p * 1) : tier("long", p * 2)',
        },
      },
      500000
    )
  ).toBeNull()
})

it('routes legacy lane conversions through the full editor rather than dropping their prices', () => {
  const entry = {
    model_name: 'quick',
    version: 'v',
    configured: {},
    effective: { ModelRatio: 1, AudioRatio: 2, AudioCompletionRatio: 3 },
  }
  expect(() =>
    quickPriceValues(
      { mode: 'token', input: '0', output: '0.125' },
      entry,
      500000
    )
  ).toThrow()
})

it('distinguishes disabled cache prices from explicit zero and preserves TTL and request rules', () => {
  const entry = {
    model_name: 'quick',
    version: 'v',
    configured: {},
    effective: {
      'billing_setting.billing_mode': 'tiered_expr',
      'billing_setting.billing_expr':
        '(tier("base", p * 2 + c * 10 + cr * 0.2 + cc * 2.5 + cc12h * 5)) * (param("fast") == true ? 2 : 1)',
    },
  }
  const value = quickPrice(entry, 500000)
  expect(value).not.toBeNull()
  if (!value) throw new Error('Expected simple token pricing')
  expect(value).toMatchObject({
    cacheRead: '0.2',
    cacheWrite: '2.5',
    longCacheWrite: true,
  })
  const edited = quickPriceValues(
    { ...value, cacheRead: '0', cacheWrite: '0.03125' },
    entry,
    500000
  )
  const expression = String(edited['billing_setting.billing_expr'])
  expect(expression).toContain('cr * 0')
  expect(expression).toContain('cc * 0.03125')
  expect(expression).toContain('cc12h * 5')
  expect(expression).toContain('param("fast")')
  const off = quickPriceValues(
    { ...value, cacheRead: null, cacheWrite: null },
    entry,
    500000
  )
  expect(off['billing_setting.billing_expr']).not.toMatch(/\b(?:cr|cc|cc12h)\b/)
  expect(off['billing_setting.billing_expr']).toContain('p * 2')
  expect(() =>
    quickPriceValues({ ...value, cacheWrite: '' }, entry, 500000)
  ).toThrow()
  expect(() =>
    quickPriceValues({ ...value, cacheRead: '-0.1' }, entry, 500000)
  ).toThrow()
})

it('sets decimal cache prices in uniform token expressions and scales all price lanes', () => {
  const values = quickPriceValues(
    {
      mode: 'token',
      input: '0.125',
      output: '0.375',
      cacheRead: '0.0125',
      cacheWrite: '0',
    },
    { model_name: 'new', version: '', configured: {}, effective: {} },
    250000
  )
  const price = quickPrice(
    {
      model_name: 'new',
      version: '',
      configured: values,
      effective: multiplyPrice(values, 2),
    },
    250000
  )
  expect(price).toMatchObject({
    input: '0.25',
    output: '0.75',
    cacheRead: '0.025',
    cacheWrite: '0',
  })
})

it('keeps fallback cache prices out of the converted expression when changing a legacy input price', () => {
  const entry = {
    model_name: 'generic',
    version: 'v',
    configured: { ModelRatio: 1 },
    effective: {
      ModelRatio: 1,
      CompletionRatio: 2,
      CacheRatio: 1,
      CreateCacheRatio: 1.25,
    },
    cache_write_mode: 'none' as const,
  }
  const converted = {
    ...entry.configured,
    'billing_setting.billing_mode': 'tiered_expr',
    'billing_setting.billing_expr': 'tier("base", p * 2 + c * 4)',
  }
  const changed = quickPriceValues(
    { mode: 'token', input: '2.5', output: '5' },
    { ...entry, effective: converted, configured: converted },
    500000
  )
  expect(changed).not.toHaveProperty('CreateCacheRatio')
  expect(changed).not.toHaveProperty('CacheRatio')
  expect(changed['billing_setting.billing_expr']).toContain('p * 2.5')
  expect(changed['billing_setting.billing_expr']).not.toMatch(/\b(?:cr|cc)\b/)
  expect(changed.ModelRatio).toBe(1)
})

it('creates inline token prices for unpriced models without treating runtime default lanes as configured prices', () => {
  const entry = {
    model_name: 'unpriced',
    version: 'v',
    configured: {},
    effective: {
      CompletionRatio: 1,
      CacheRatio: 1,
      CreateCacheRatio: 1.25,
      ImageRatio: 1,
    },
  }
  const prices = quickPriceValues(
    {
      mode: 'token',
      input: '2',
      output: '10',
      cacheRead: '0.0125',
      cacheWrite: '2.5',
    },
    entry,
    500000
  )
  expect(prices['billing_setting.billing_mode']).toBe('tiered_expr')
  expect(prices['billing_setting.billing_expr']).toContain('p * 2')
  expect(prices['billing_setting.billing_expr']).toContain('cr * 0.0125')
  expect(prices['billing_setting.billing_expr']).toContain('cc * 2.5')
  expect(prices).not.toHaveProperty('CreateCacheRatio')
  expect(() =>
    quickPriceValues(
      { mode: 'token', input: '2', output: '10' },
      {
        ...entry,
        configured: { AudioRatio: 2 },
        effective: { ...entry.effective, AudioRatio: 2 },
      },
      500000
    )
  ).toThrow()
})
