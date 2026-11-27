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
import { t } from 'i18next'

import type { ModelPricingEntry } from '@/features/model-pricing/api'
import type { PricingValues } from '@/features/model-pricing/pricing'
import {
  combineBillingExpr,
  splitBillingExprAndRequestRules,
} from '@/features/pricing/lib/billing-expr'
import {
  parseVisualBillingDocument,
  serializeVisualBillingDocument,
} from '@/features/pricing/lib/billing-expression/visual'
import { formatPricingNumber } from '@/features/system-settings/models/pricing-format'

export type QuickPrice = {
  mode: 'token' | 'request'
  input: string
  output: string
  cacheRead?: string | null
  cacheWrite?: string | null
  longCacheWrite?: boolean
}

export function quickPrice(
  entry: ModelPricingEntry,
  quotaPerUnit: number
): QuickPrice | null {
  const values = entry.effective
  if (
    entry.usage_schema ||
    Object.keys(values['billing_setting.plugin_billing_expr'] ?? {}).length
  ) {
    return null
  }
  if (values['billing_setting.billing_mode'] === 'tiered_expr') {
    const split = splitBillingExprAndRequestRules(
      String(values['billing_setting.billing_expr'] ?? '')
    )
    const doc = parseVisualBillingDocument(split.billingExpr)
    if (!doc || doc.root.kind !== 'tier' || doc.imageCount) return null
    if (doc.root.billingUnit === 'request') {
      return { mode: 'request', input: doc.root.fixedPrice, output: '' }
    }
    return {
      mode: 'token',
      input: doc.root.prices.find((p) => p.variable === 'p')?.value ?? '0',
      output: doc.root.prices.find((p) => p.variable === 'c')?.value ?? '0',
      cacheRead:
        doc.root.prices.find((p) => p.variable === 'cr')?.value ?? null,
      cacheWrite:
        doc.root.prices.find((p) => p.variable === 'cc')?.value ?? null,
      longCacheWrite: doc.root.prices.some((p) =>
        ['cc1h', 'cc12h'].includes(p.variable)
      ),
    }
  }
  if (values.ModelPrice !== undefined) {
    return { mode: 'request', input: String(values.ModelPrice), output: '' }
  }
  if (values.ModelRatio === undefined) {
    return {
      mode: 'token',
      input: '',
      output: '',
      cacheRead: null,
      cacheWrite: null,
    }
  }
  const input = (Number(values.ModelRatio) * 1000000) / quotaPerUnit
  return {
    mode: 'token',
    input: formatPricingNumber(input),
    output: formatPricingNumber(input * Number(values.CompletionRatio ?? 1)),
    cacheRead:
      values.CacheRatio !== undefined && Number(values.CacheRatio) !== 1
        ? formatPricingNumber(input * Number(values.CacheRatio))
        : null,
    cacheWrite:
      (entry.cache_write_mode && entry.cache_write_mode !== 'none') ||
      entry.configured.CreateCacheRatio !== undefined
        ? formatPricingNumber(input * Number(values.CreateCacheRatio ?? 1.25))
        : null,
    longCacheWrite: ['claude_ttl', 'openai_ttl'].includes(
      entry.cache_write_mode ?? ''
    ),
  }
}

export function quickPriceValues(
  price: QuickPrice,
  entry: ModelPricingEntry,
  quotaPerUnit: number
): PricingValues {
  const hasCacheEdit =
    price.mode === 'token' &&
    (price.cacheRead !== undefined || price.cacheWrite !== undefined)
  const amounts =
    price.mode === 'token'
      ? [
          price.input,
          price.output,
          ...[price.cacheRead, price.cacheWrite].filter(
            (v): v is string => typeof v === 'string'
          ),
        ]
      : [price.input]
  for (const value of amounts) {
    if (!value.trim() || !Number.isFinite(Number(value)) || Number(value) < 0) {
      throw new Error(t('Enter a finite, non-negative price'))
    }
  }
  const values = { ...entry.effective, ...entry.configured }
  const previous = quickPrice(entry, quotaPerUnit)
  if (!previous) throw new Error(t('Edit model pricing'))
  // Existing token pricing must first pass through the server's conversion
  // contract, which resolves default and model-specific billing lanes.
  if (
    values['billing_setting.billing_mode'] !== 'tiered_expr' &&
    price.mode === 'token' &&
    previous.mode === price.mode &&
    [
      'ModelRatio',
      'CacheRatio',
      'CreateCacheRatio',
      'ImageRatio',
      'AudioRatio',
      'AudioCompletionRatio',
    ].some(
      (key) =>
        Object.hasOwn(values, key) &&
        (values.ModelRatio !== undefined ||
          Object.hasOwn(entry.configured, key))
    )
  ) {
    throw new Error(t('Edit model pricing'))
  }
  const split = splitBillingExprAndRequestRules(
    values['billing_setting.billing_mode'] === 'tiered_expr'
      ? String(values['billing_setting.billing_expr'] ?? '')
      : ''
  )
  const doc = parseVisualBillingDocument(split.billingExpr)
  let expression =
    price.mode === 'request'
      ? `tier("request", fixed(${Number(price.input)}))`
      : `tier("base", p * ${Number(price.input)} + c * ${Number(price.output)})`
  if (
    doc?.root.kind === 'tier' &&
    !doc.imageCount &&
    previous?.mode === price.mode
  ) {
    if (price.mode === 'request') doc.root.fixedPrice = price.input
    else {
      doc.root.prices = doc.root.prices.filter(
        (p) => p.variable !== 'p' && p.variable !== 'c'
      )
      doc.root.prices.push(
        { variable: 'p', value: price.input },
        { variable: 'c', value: price.output }
      )
    }
    const result = serializeVisualBillingDocument(doc)
    if (!result.ok) throw new Error(t('Invalid pricing value'))
    expression = result.source
  }
  if (hasCacheEdit) {
    const edited = parseVisualBillingDocument(expression)
    if (!edited || edited.root.kind !== 'tier') {
      throw new Error(t('Edit model pricing'))
    }
    for (const [variable, amount] of [
      ['cr', price.cacheRead],
      ['cc', price.cacheWrite],
    ] as const) {
      if (amount === undefined) continue
      edited.root.prices = edited.root.prices.filter(
        (p) =>
          p.variable !== variable &&
          !(
            variable === 'cc' &&
            amount === null &&
            ['cc1h', 'cc12h'].includes(p.variable)
          )
      )
      if (amount !== null) edited.root.prices.push({ variable, value: amount })
    }
    const result = serializeVisualBillingDocument(edited)
    if (!result.ok) throw new Error(t('Invalid pricing value'))
    expression = result.source
  }
  return {
    ...entry.configured,
    'billing_setting.billing_mode': 'tiered_expr',
    'billing_setting.billing_expr': combineBillingExpr(
      expression,
      split.requestRuleExpr
    ),
  }
}
