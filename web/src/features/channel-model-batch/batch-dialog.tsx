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
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import type { ModelPricingConfig } from '@/features/model-pricing/api'
import {
  pricingFromDraft,
  pricingRow,
  type PricingValues,
} from '@/features/model-pricing/pricing'
import type { Vendor } from '@/features/models/types'
import {
  ModelPricingEditorPanel,
  type ModelPricingEditorPanelHandle,
} from '@/features/system-settings/models/model-pricing-sheet'
import { handleServerError } from '@/lib/handle-server-error'
import { getLobeIcon } from '@/lib/lobe-icon'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { previewBatchPricing, type BatchSnapshot, type DraftRow } from './api'
import { draftEntry, multiplyPrice } from './draft'
import { EndpointSelect } from './endpoint-select'
import {
  parseEndpointConfig,
  updateEndpointConfig,
  type EndpointConfig,
} from './endpoints'
import { CachePriceFields, QuickPriceFields } from './inline-pricing'
import { PricingSummary } from './pricing-summary'
import { quickPriceValues, type QuickPrice } from './quick-pricing'

export function BatchDialog(props: {
  rows: DraftRow[]
  initialMode?: string
  vendors: Vendor[]
  snapshot: BatchSnapshot
  endpointOptions: EndpointConfig
  pricing: ModelPricingConfig
  onApply: (rows: DraftRow[]) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [vendor, setVendor] = useState('keep')
  const [mode, setMode] = useState(props.initialMode ?? 'keep')
  const [visibility, setVisibility] = useState('keep')
  const [endpointMode, setEndpointMode] = useState('keep')
  const [endpoints, setEndpoints] = useState('')
  const quotaPerUnit = useSystemConfigStore(
    (s) => s.config.currency.quotaPerUnit
  )
  const [quick, setQuick] = useState<QuickPrice>({
    mode: 'token',
    input: '',
    output: '',
    cacheRead: null,
    cacheWrite: null,
  })
  const [factor, setFactor] = useState('1')
  const [preparing, setPreparing] = useState(false)
  const [preview, setPreview] = useState<DraftRow[] | null>(null)
  const editor = useRef<ModelPricingEditorPanelHandle>(null)
  const first = draftEntry(props.rows[0], props.pricing)
  const [editData] = useState(() =>
    pricingRow(first.model_name, first.effective)
  )
  const prepare = async () => {
    setPreparing(true)
    try {
      if (
        endpointMode === 'set' &&
        !Object.keys(parseEndpointConfig(endpoints)).length
      ) {
        throw new Error(t('Select at least one endpoint'))
      }
      let uniform: PricingValues | undefined
      if (mode === 'uniform') {
        const data = await editor.current?.commitDraft()
        if (!data) return
        uniform = pricingFromDraft(data)
        if (props.rows.length > 1) {
          delete uniform['billing_setting.plugin_billing_expr']
        }
      }
      const rows = props.rows.map((row) => {
        const result = { ...row }
        if (mode !== 'keep') {
          delete result.quickPricing
          delete result.priceError
          delete result.previewPricing
          delete result.pricingBase
        }
        if (vendor !== 'keep') {
          result.vendorId = Number(vendor)
          result.vendorTouched = true
        }
        if (visibility !== 'keep') {
          result.squareVisible = visibility === 'show'
          result.squareTouched = true
        }
        if (endpointMode !== 'keep') {
          // Apply types uniformly, retaining each model's own custom paths.
          result.endpoints =
            endpointMode === 'auto'
              ? ''
              : updateEndpointConfig(
                  row.endpoints ?? row.metadata?.endpoints ?? '',
                  Object.keys(parseEndpointConfig(endpoints)),
                  props.snapshot,
                  props.endpointOptions
                )
          result.endpointsTouched = true
        }
        if (mode === 'token' || mode === 'request') {
          const entry = draftEntry(row, props.pricing)
          // Uniform mode is an explicit replacement, unlike inline edits. Keep
          // provider overrides while replacing the model-level price.
          result.pricing = quickPriceValues(
            { ...quick, mode },
            {
              ...entry,
              effective: {},
              configured: entry.configured[
                'billing_setting.plugin_billing_expr'
              ]
                ? {
                    'billing_setting.plugin_billing_expr':
                      entry.configured['billing_setting.plugin_billing_expr'],
                  }
                : {},
            },
            quotaPerUnit
          )
        }
        if (uniform) {
          result.pricing = { ...uniform }
          const own = draftEntry(row, props.pricing).configured[
            'billing_setting.plugin_billing_expr'
          ]
          if (own && props.rows.length > 1) {
            result.pricing['billing_setting.plugin_billing_expr'] = own
          }
        }
        if (mode === 'multiply') {
          const entry = draftEntry(row, props.pricing)
          result.pricing = multiplyPrice(
            { ...entry.effective, ...entry.configured },
            Number(factor)
          )
          if (
            entry.effective['billing_setting.billing_mode'] !== 'tiered_expr'
          ) {
            for (const key of [
              'CacheRatio',
              'CreateCacheRatio',
              'ImageRatio',
            ] as const) {
              if (entry.configured[key] === undefined) {
                delete result.pricing[key]
              }
            }
          }
        }
        if (result.pricing) {
          result.pricingVersion =
            row.pricingVersion ?? draftEntry(row, props.pricing).version
        }
        return result
      })
      setPreview(await previewBatchPricing(rows))
    } catch (error) {
      handleServerError(error)
    } finally {
      setPreparing(false)
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Batch settings')}
      description={t('Selected models: {{count}}', {
        count: props.rows.length,
      })}
      contentClassName={
        mode === 'uniform' || preview
          ? 'channel-batch-dialog sm:max-w-4xl'
          : 'channel-batch-dialog sm:max-w-xl'
      }
      footer={
        <>
          <Button variant='outline' onClick={props.onClose}>
            {t('Cancel')}
          </Button>
          {preview ? (
            <>
              <Button variant='outline' onClick={() => setPreview(null)}>
                {t('Back')}
              </Button>
              <Button
                onClick={() => {
                  props.onApply(preview)
                  props.onClose()
                  toast.success(t('Changes applied to draft'))
                }}
              >
                {t('Apply to draft')}
              </Button>
            </>
          ) : (
            <Button disabled={preparing} onClick={() => void prepare()}>
              {t('Preview changes')}
            </Button>
          )}
        </>
      }
    >
      {preview ? (
        <div className='space-y-3'>
          <p className='text-muted-foreground text-sm'>
            {t('Review selected models before applying. Nothing is saved yet.')}
          </p>
          <StaticDataTable
            tableClassName='table-fixed [&_td]:whitespace-normal [&_th]:whitespace-normal'
            data={preview}
            getRowKey={(row) => row.source_name}
            columns={[
              {
                id: 'model',
                header: t('Model'),
                cell: (row) => <span className='break-all'>{row.name}</span>,
              },
              {
                id: 'vendor',
                header: t('Vendor'),
                cell: (row) =>
                  props.vendors.find((item) => item.id === row.vendorId)
                    ?.name ?? t('Unassigned'),
              },
              {
                id: 'square',
                header: t('Square display'),
                cell: (row) => {
                  const before = props.rows.find(
                    (item) => item.source_name === row.source_name
                  )
                  return (
                    <span>
                      {before?.squareVisible ? t('Shown') : t('Hidden')} →{' '}
                      {row.squareVisible ? t('Shown') : t('Hidden')}
                    </span>
                  )
                },
              },
              {
                id: 'endpoints',
                header: t('Endpoint selection'),
                cell: (row) => (
                  <span className='break-words'>
                    {Object.keys(
                      parseEndpointConfig(
                        props.rows.find(
                          (item) => item.source_name === row.source_name
                        )?.endpoints ?? ''
                      )
                    ).join(', ') || t('Automatic by channel')}
                    {' → '}
                    {Object.keys(parseEndpointConfig(row.endpoints ?? '')).join(
                      ', '
                    ) || t('Automatic by channel')}
                  </span>
                ),
              },
              {
                id: 'before',
                header: t('Before'),
                cell: (row) => (
                  <PricingSummary
                    entry={draftEntry(
                      props.rows.find(
                        (item) => item.source_name === row.source_name
                      ) ?? row,
                      props.pricing
                    )}
                  />
                ),
              },
              {
                id: 'after',
                header: t('After'),
                cell: (row) => (
                  <PricingSummary entry={draftEntry(row, props.pricing)} />
                ),
              },
            ]}
          />
        </div>
      ) : (
        <div className='space-y-5'>
          <div className='space-y-2'>
            <Label htmlFor='batch-vendor'>{t('Vendor')}</Label>
            <Combobox
              id='batch-vendor'
              value={vendor}
              onValueChange={(value) => setVendor(value ?? 'keep')}
              searchPlaceholder={t('Search vendors')}
              options={[
                { value: 'keep', label: t('Keep existing vendor') },
                ...props.vendors.map((item) => ({
                  value: String(item.id),
                  label: item.name,
                  icon: getLobeIcon(item.icon, 20),
                })),
              ]}
            />
            <p className='text-muted-foreground text-xs'>
              {t('Model icons inherit the selected vendor icon.')}
            </p>
          </div>
          <div className='grid gap-4 sm:grid-cols-2'>
            <div className='space-y-2'>
              <Label htmlFor='batch-square'>{t('Square display')}</Label>
              <Combobox
                id='batch-square'
                value={visibility}
                onValueChange={(value) => setVisibility(value ?? 'keep')}
                options={[
                  { value: 'keep', label: t('Keep existing visibility') },
                  { value: 'show', label: t('Shown') },
                  { value: 'hide', label: t('Hidden') },
                ]}
              />
            </div>
            <div className='space-y-2'>
              <Label htmlFor='batch-endpoint-mode'>
                {t('Endpoint selection')}
              </Label>
              <Combobox
                id='batch-endpoint-mode'
                value={endpointMode}
                onValueChange={(value) => setEndpointMode(value ?? 'keep')}
                options={[
                  { value: 'keep', label: t('Keep existing endpoints') },
                  { value: 'set', label: t('Set endpoints') },
                  { value: 'auto', label: t('Automatic by channel') },
                ]}
              />
            </div>
          </div>
          {endpointMode === 'set' && (
            <EndpointSelect
              value={endpoints}
              snapshot={props.snapshot}
              catalog={props.endpointOptions}
              label={t('Select endpoints')}
              onChange={setEndpoints}
            />
          )}
          <p className='text-muted-foreground text-xs'>
            {t(
              'Endpoint metadata supplements channel capabilities; it does not disable API access. Empty selection uses channel defaults.'
            )}
          </p>
          <div className='space-y-2'>
            <Label htmlFor='batch-price-mode'>{t('Pricing')}</Label>
            <Combobox
              id='batch-price-mode'
              value={mode}
              onValueChange={(value) => setMode(value ?? 'keep')}
              options={[
                { value: 'keep', label: t('Keep existing prices') },
                { value: 'multiply', label: t('Multiply existing prices') },
                { value: 'token', label: t('Set uniform token prices') },
                { value: 'request', label: t('Set uniform request price') },
                { value: 'uniform', label: t('Tiered / advanced') },
              ]}
            />
          </div>
          {(mode === 'token' || mode === 'request') && (
            <div className='space-y-3'>
              <Label className='block'>{t('Customer pricing')} · USD</Label>
              <QuickPriceFields
                value={{ ...quick, mode }}
                name={t('Selected models')}
                onChange={setQuick}
              />
              {mode === 'token' && (
                <div className='border-t pt-4'>
                  <CachePriceFields
                    value={quick}
                    name={t('Selected models')}
                    onChange={setQuick}
                  />
                </div>
              )}
            </div>
          )}
          {mode === 'multiply' && (
            <div className='space-y-2'>
              <Label htmlFor='batch-price-factor'>{t('Price factor')}</Label>
              <Input
                id='batch-price-factor'
                inputMode='decimal'
                value={factor}
                onChange={(event) => setFactor(event.target.value)}
              />
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Only configured price amounts change. Missing prices and unsupported expressions block the batch.'
                )}
              </p>
            </div>
          )}
          {mode === 'uniform' && (
            <ModelPricingEditorPanel
              ref={editor}
              embedded
              editData={editData}
              usageSchema={
                props.rows.length === 1 ? first.usage_schema : undefined
              }
              pluginVariants={
                props.rows.length === 1 ? first.plugin_variants : undefined
              }
              onSave={prepare}
            />
          )}
        </div>
      )}
    </Dialog>
  )
}
