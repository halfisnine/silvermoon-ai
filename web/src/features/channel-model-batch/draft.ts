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

import type {
  ModelPricingConfig,
  ModelPricingEntry,
} from '@/features/model-pricing/api'
import type { PricingValues } from '@/features/model-pricing/pricing'
import type { Model } from '@/features/models/types'
import { adjustSyncPrice } from '@/features/system-settings/models/upstream-price-adjustment'
import type { PricingSyncValues } from '@/features/system-settings/types'

import type { BatchChange, BatchSnapshot, DraftRow } from './api'

export function resolveMetadata(
  records: Model[],
  name: string
): Model | undefined {
  return (
    records.find(
      (record) => record.name_rule === 0 && record.model_name === name
    ) ??
    records.find(
      (record) => record.name_rule === 1 && name.startsWith(record.model_name)
    ) ??
    records.find(
      (record) => record.name_rule === 3 && name.endsWith(record.model_name)
    ) ??
    records.find(
      (record) => record.name_rule === 2 && name.includes(record.model_name)
    )
  )
}

export function createDrafts(snapshot: BatchSnapshot): DraftRow[] {
  return snapshot.rows.map((row) => ({
    ...row,
    name: row.source_name,
    vendorId: row.metadata?.vendor_id ?? 0,
    squareVisible: row.metadata?.status === 1,
    endpoints: row.metadata?.endpoints ?? '',
  }))
}

export function renameDraft(
  row: DraftRow,
  name: string,
  snapshot: BatchSnapshot,
  config: ModelPricingConfig
): DraftRow {
  const metadata = resolveMetadata(snapshot.metadata, name)
  let squareVisible = row.squareVisible
  if (!row.squareTouched) {
    squareVisible =
      name === row.source_name
        ? row.metadata?.status === 1
        : snapshot.metadata.some(
            (record) =>
              record.name_rule === 0 &&
              record.model_name === name &&
              record.status === 1
          )
  }
  const next = {
    ...row,
    name,
    vendorId: row.vendorTouched
      ? row.vendorId
      : (metadata?.vendor_id ?? row.metadata?.vendor_id ?? 0),
    squareVisible,
    endpoints: row.endpointsTouched
      ? row.endpoints
      : (snapshot.metadata.find(
          (record) => record.name_rule === 0 && record.model_name === name
        )?.endpoints ??
        row.metadata?.endpoints ??
        ''),
  }
  if (row.pricing || row.quickPricing) {
    // Price drafts travel with the row, but conflict versions belong to names.
    // Remember visited names so renaming back cannot refresh an old draft's
    // version and silently overwrite a concurrent administrator's changes.
    const version =
      row.pricingVersion ??
      config.entries.find((entry) => entry.model_name === row.name)?.version
    next.pricingVersions = {
      ...row.pricingVersions,
      ...(version !== undefined ? { [row.name]: version } : {}),
    }
    next.pricingVersion = Object.hasOwn(next.pricingVersions, name)
      ? next.pricingVersions[name]
      : config.entries.find((entry) => entry.model_name === name)?.version
  }
  return next
}

export function isDirty(row: DraftRow): boolean {
  return (
    row.name !== row.source_name ||
    row.vendorId !== (row.metadata?.vendor_id ?? 0) ||
    row.vendorTouched === true ||
    row.squareTouched === true ||
    row.endpointsTouched === true ||
    row.pricing !== undefined ||
    row.quickPricing !== undefined
  )
}

export function validateDrafts(
  rows: DraftRow[],
  snapshot: BatchSnapshot
): string | null {
  if (rows.filter(isDirty).length > 1000) {
    return t('Select between 1 and 1000 models')
  }
  const names = new Set<string>()
  const original = new Set(snapshot.rows.map((row) => row.source_name))
  const mapping = JSON.parse(snapshot.channel.model_mapping || '{}') as Record<
    string,
    string
  >
  for (const row of rows) {
    if (row.priceError) return row.priceError
    if (
      !row.name ||
      /[\s,\p{Cc}]/u.test(row.name) ||
      [...row.name].length > 128
    ) {
      return t(
        'Enter a model name of up to 128 characters without commas or whitespace.'
      )
    }
    if (names.has(row.name)) return t('Duplicate customer model name')
    names.add(row.name)
    if (
      row.name !== row.source_name &&
      (original.has(row.name) || Object.hasOwn(mapping, row.name))
    ) {
      return t('Customer model name is already used by this channel')
    }
    if (
      row.vendorId !== 0 &&
      !snapshot.vendors.some((vendor) => vendor.id === row.vendorId)
    ) {
      return t('Select a saved vendor.')
    }
  }
  return null
}

export function buildChanges(
  rows: DraftRow[],
  config: ModelPricingConfig
): BatchChange[] {
  return rows.filter(isDirty).map((row) => ({
    source_name: row.source_name,
    model_name: row.name,
    vendor_id: row.vendorId,
    status: (row.squareVisible ?? row.metadata?.status === 1) ? 1 : 0,
    ...(row.endpointsTouched ? { endpoints: row.endpoints ?? '' } : {}),
    ...(row.pricing === undefined
      ? {}
      : {
          pricing: row.pricing,
          expected_pricing_version:
            row.pricingVersion ??
            config.entries.find((entry) => entry.model_name === row.name)
              ?.version ??
            config.empty_version,
        }),
  }))
}

// Reuse the expression AST adjustment path: only price leaves change; tier
// thresholds, usage quantities, units and request multipliers stay intact.
export function multiplyPrice(
  values: PricingValues,
  factor: number
): PricingValues {
  if (!Number.isFinite(factor) || factor <= 0) {
    throw new Error(t('Enter a positive price factor'))
  }
  const wire: PricingSyncValues = {}
  const keys = {
    ModelPrice: 'model_price',
    ModelRatio: 'model_ratio',
    CompletionRatio: 'completion_ratio',
    CacheRatio: 'cache_ratio',
    CreateCacheRatio: 'create_cache_ratio',
    ImageRatio: 'image_ratio',
    AudioRatio: 'audio_ratio',
    AudioCompletionRatio: 'audio_completion_ratio',
    'billing_setting.billing_mode': 'billing_mode',
    'billing_setting.billing_expr': 'billing_expr',
  } as const
  for (const [key, field] of Object.entries(keys)) {
    const value = values[key as keyof typeof keys]
    if (value !== undefined) wire[field] = value
  }
  const adjusted = adjustSyncPrice(wire, 'multiply', factor)
  if (adjusted.error || !adjusted.values) {
    throw new Error(t(adjusted.error ?? 'Unset price'))
  }
  const next = { ...values }
  for (const [key, field] of Object.entries(keys)) {
    const value = adjusted.values[field]
    if (value !== undefined) next[key as keyof typeof keys] = value
  }
  // Provider overrides cannot be safely adjusted as token expressions. A
  // model with provider prices must use the existing provider-aware editor.
  if (Object.keys(values['billing_setting.plugin_billing_expr'] ?? {}).length) {
    throw new Error(
      t('Edit provider prices individually before multiplying this model.')
    )
  }
  return next
}

export function draftEntry(
  row: DraftRow,
  config: ModelPricingConfig
): ModelPricingEntry {
  const entry = config.entries.find((item) => item.model_name === row.name)
  if (row.pricing) {
    return {
      ...entry,
      model_name: row.name,
      version: entry?.version ?? config.empty_version,
      configured: row.pricing,
      effective: row.previewPricing ?? row.pricing,
    }
  }
  return (
    row.pricingBase ??
    entry ?? {
      model_name: row.name,
      version: config.empty_version,
      configured: {},
      effective: {},
    }
  )
}
