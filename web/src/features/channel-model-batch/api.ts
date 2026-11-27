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
import {
  getModelPricing,
  previewModelPricing,
  type ModelPricingConfig,
} from '@/features/model-pricing/api'
import type { PricingValues } from '@/features/model-pricing/pricing'
import type { Model, Vendor } from '@/features/models/types'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

export type BatchChannel = {
  id: number
  name: string
  type: number
  models: string
  model_mapping: string | null
}
export type BatchRow = {
  source_name: string
  upstream_name: string
  metadata: Model | null
}
export type BatchSnapshot = {
  channel: BatchChannel
  rows: BatchRow[]
  vendors: Vendor[]
  metadata: Model[]
  channels: BatchChannel[]
  version: string
}
export type BatchChange = {
  source_name: string
  model_name: string
  vendor_id?: number
  pricing?: PricingValues
  expected_pricing_version?: string
}
export type DraftRow = BatchRow & {
  name: string
  vendorId: number
  vendorTouched?: boolean
  quickPricing?: import('./quick-pricing').QuickPrice
  priceError?: string
  pricing?: PricingValues
  pricingVersion?: string
  pricingVersions?: Record<string, string>
  pricingBase?: import('@/features/model-pricing/api').ModelPricingEntry
  previewPricing?: PricingValues
}

// Keep filtered reads below ordinary reverse-proxy URL limits, even for long
// Unicode model names. Four requests at a time bound load for large channels.
export async function getBatchPricing(
  names: string[]
): Promise<ModelPricingConfig> {
  let chunk: string[] = []
  const chunks: string[][] = [chunk]
  let length = 0
  for (const name of names) {
    const size = new URLSearchParams({ model: name }).toString().length + 1
    if (length + size > 3000 && chunk.length) {
      chunk = []
      chunks.push(chunk)
      length = 0
    }
    chunk.push(name)
    length += size
  }
  const results: ModelPricingConfig[] = []
  for (let start = 0; start < chunks.length; start += 4) {
    results.push(
      ...(await Promise.all(
        chunks.slice(start, start + 4).map(getModelPricing)
      ))
    )
  }
  return { ...results[0], entries: results.flatMap((result) => result.entries) }
}

export async function previewBatchPricing(
  rows: DraftRow[]
): Promise<DraftRow[]> {
  const results: DraftRow[] = []
  for (let start = 0; start < rows.length; start += 8) {
    results.push(
      ...(await Promise.all(
        rows.slice(start, start + 8).map(async (row) => {
          if (!row.pricing) return row
          const preview = await previewModelPricing({
            model_name: row.name,
            pricing: row.pricing,
          })
          return { ...row, previewPricing: preview.effective }
        })
      ))
    )
  }
  return results
}

export async function getBatchSnapshot(id: number): Promise<BatchSnapshot> {
  const response = await api.get(`/api/channel/${id}/model_batch`)
  return requireServerSuccess(response.data).data
}
export async function saveBatch(
  id: number,
  version: string,
  changes: BatchChange[]
): Promise<void> {
  const response = await api.post(`/api/channel/${id}/model_batch`, {
    expected_version: version,
    changes,
  })
  requireServerSuccess(response.data)
}
