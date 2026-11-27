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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Combobox } from '@/components/ui/combobox'
import {
  InputGroup,
  InputGroupInput,
  InputGroupAddon,
} from '@/components/ui/input-group'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import {
  previewModelPricingConversion,
  type ModelPricingConfig,
  type ModelPricingEntry,
} from '@/features/model-pricing/api'
import { useSystemConfigStore } from '@/stores/system-config-store'

import type { DraftRow } from './api'
import { draftEntry } from './draft'
import { quickPrice, quickPriceValues, type QuickPrice } from './quick-pricing'

export function QuickPriceFields(props: {
  value: QuickPrice
  name: string
  onChange: (value: QuickPrice) => void
  disabled?: boolean
  errorId?: string
}) {
  const { t } = useTranslation()
  return (
    <div className='batch-price-fields'>
      <InputGroup>
        <InputGroupInput
          inputMode='decimal'
          disabled={props.disabled}
          aria-label={t('Input price for {{model}}', { model: props.name })}
          aria-invalid={
            props.errorId
              ? !props.value.input.trim() ||
                !Number.isFinite(Number(props.value.input)) ||
                Number(props.value.input) < 0
              : undefined
          }
          aria-describedby={props.errorId}
          value={props.value.input}
          onChange={(e) =>
            props.onChange({ ...props.value, input: e.target.value })
          }
          placeholder='0.00'
        />
        <InputGroupAddon align='inline-end'>
          {props.value.mode === 'request' ? t('/ request') : t('/ M input')}
        </InputGroupAddon>
      </InputGroup>
      {props.value.mode === 'token' && (
        <InputGroup>
          <InputGroupInput
            inputMode='decimal'
            disabled={props.disabled}
            aria-label={t('Output price for {{model}}', { model: props.name })}
            aria-invalid={
              props.errorId
                ? !props.value.output.trim() ||
                  !Number.isFinite(Number(props.value.output)) ||
                  Number(props.value.output) < 0
                : undefined
            }
            aria-describedby={props.errorId}
            value={props.value.output}
            onChange={(e) =>
              props.onChange({ ...props.value, output: e.target.value })
            }
            placeholder='0.00'
          />
          <InputGroupAddon align='inline-end'>
            {t('/ M output')}
          </InputGroupAddon>
        </InputGroup>
      )}
    </div>
  )
}

export function CachePriceFields(props: {
  value: QuickPrice
  name: string
  onChange: (value: QuickPrice) => void
  disabled?: boolean
  errorId?: string
  compact?: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  return (
    <div className={props.compact ? 'batch-cache-fields' : 'space-y-3'}>
      <div
        className={
          props.compact ? 'batch-cache-grid' : 'grid gap-3 sm:grid-cols-2'
        }
      >
        {(['cacheRead', 'cacheWrite'] as const).map((field) => {
          const enabled =
            props.value[field] != null ||
            (field === 'cacheWrite' && props.value.longCacheWrite)
          const label =
            field === 'cacheRead'
              ? t('Cache read price')
              : t('Cache write price')
          let displayLabel = label
          if (props.compact) {
            displayLabel =
              field === 'cacheRead' ? t('Read cache') : t('Write cache')
          }
          const accessibleLabel = t('{{field}} for {{model}}', {
            field: label,
            model: props.name,
          })
          const toggle = (
            <div className='flex items-center gap-2 text-xs'>
              <Switch
                size='sm'
                id={`${id}-${field}`}
                aria-label={accessibleLabel}
                checked={!!enabled}
                disabled={props.disabled}
                onCheckedChange={(checked) =>
                  props.onChange({
                    ...props.value,
                    [field]: checked ? '' : null,
                  })
                }
              />
              <Label
                htmlFor={`${id}-${field}`}
                className='text-xs'
                title={label}
              >
                <span aria-hidden='true'>{displayLabel}</span>
                <span className='sr-only'>{accessibleLabel}</span>
              </Label>
            </div>
          )
          return (
            <div
              className={props.compact ? 'min-w-0' : 'min-w-0 space-y-2'}
              key={field}
            >
              {!props.compact && toggle}
              <InputGroup
                className={
                  props.compact && !props.disabled
                    ? 'has-disabled:bg-transparent has-disabled:opacity-100'
                    : undefined
                }
                title={
                  props.compact
                    ? t(
                        'Off: use the input price. On: use the cache price; 0 means free. Long-lived writes follow system TTL rules.'
                      )
                    : undefined
                }
              >
                {props.compact && (
                  <InputGroupAddon align='inline-start'>
                    {toggle}
                  </InputGroupAddon>
                )}
                <InputGroupInput
                  inputMode='decimal'
                  aria-label={accessibleLabel}
                  disabled={props.disabled || !enabled}
                  aria-invalid={
                    props.errorId && enabled
                      ? !(props.value[field] ?? '').trim() ||
                        !Number.isFinite(Number(props.value[field])) ||
                        Number(props.value[field]) < 0
                      : undefined
                  }
                  aria-describedby={enabled ? props.errorId : undefined}
                  value={props.value[field] ?? ''}
                  placeholder={enabled ? '0.00' : t('Use input price')}
                  data-cache-price='true'
                  title={!enabled ? t('Use input price') : undefined}
                  onChange={(event) =>
                    props.onChange({
                      ...props.value,
                      [field]: event.target.value,
                    })
                  }
                />
                <InputGroupAddon align='inline-end'>
                  {props.compact ? '/ 1M' : t('/ 1M tokens')}
                </InputGroupAddon>
              </InputGroup>
            </div>
          )
        })}
      </div>
      {!props.compact && (
        <p className='text-muted-foreground text-xs leading-relaxed'>
          {t(
            'Off: use the input price. On: use the cache price; 0 means free. Long-lived writes follow system TTL rules.'
          )}
        </p>
      )}
      {!props.compact && props.value.longCacheWrite && (
        <p className='text-muted-foreground text-xs'>
          {t(
            'Long-lived write prices are preserved when editing the standard price. Turning off writes removes all separate write prices. Use advanced settings for TTL prices.'
          )}
        </p>
      )}
    </div>
  )
}

export function InlinePricing(props: {
  row: DraftRow
  pricing: ModelPricingConfig
  busy: boolean
  onPatch: (
    source: string,
    values: Partial<DraftRow>,
    expectedQuickPricing?: QuickPrice
  ) => void
  onEdit: () => void
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const quotaPerUnit = useSystemConfigStore(
    (s) => s.config.currency.quotaPerUnit
  )
  const errorId = useId()
  const [prepareConversion, setPrepareConversion] = useState(false)
  const entry = draftEntry(props.row, props.pricing)
  const pricingVersion =
    props.row.pricingVersion ??
    props.pricing.entries.find((item) => item.model_name === props.row.name)
      ?.version ??
    (props.busy ? undefined : props.pricing.empty_version)
  const waitingForPrice =
    props.busy &&
    !props.row.pricing &&
    !props.row.quickPricing &&
    !props.pricing.entries.some((item) => item.model_name === props.row.name)
  const legacyPricing =
    entry.effective['billing_setting.billing_mode'] !== 'tiered_expr' &&
    (entry.effective.ModelRatio !== undefined ||
      entry.effective.ModelPrice !== undefined)
  const value = props.row.quickPricing ?? quickPrice(entry, quotaPerUnit)
  const conversionQuery = {
    queryKey: [
      'batch-pricing-conversion',
      entry.model_name,
      entry.effective,
      entry.configured,
    ],
    queryFn: () =>
      previewModelPricingConversion({
        model_name: entry.model_name,
        pricing: entry.configured,
      }),
    retry: false as const,
    meta: { errorToast: false },
  }
  const conversion = useQuery({
    ...conversionQuery,
    enabled: prepareConversion && legacyPricing,
  })
  const change = async (next: QuickPrice, cacheEdit = false) => {
    const saved = quickPrice(entry, quotaPerUnit)
    const updateCache =
      cacheEdit ||
      (next.mode === 'token' &&
        saved?.mode === 'token' &&
        (next.cacheRead !== saved.cacheRead ||
          next.cacheWrite !== saved.cacheWrite))
    let expectedQuickPricing: QuickPrice | undefined
    try {
      let target: ModelPricingEntry = entry
      if (legacyPricing && saved?.mode === next.mode) {
        if (!conversion.data) {
          expectedQuickPricing = next
          props.onPatch(props.row.source_name, {
            quickPricing: next,
            priceError: t('Preparing conversion...'),
            pricingBase: entry,
            pricingVersion,
          })
        }
        const prepared =
          conversion.data ?? (await client.fetchQuery(conversionQuery))
        if (!prepared?.expression) {
          throw new Error(
            prepared?.unsupported_reason || t('Edit model pricing')
          )
        }
        const converted = {
          ...entry.configured,
          'billing_setting.billing_mode': 'tiered_expr',
          'billing_setting.billing_expr': prepared.expression,
        }
        target = { ...entry, configured: converted, effective: converted }
      }
      const prices = updateCache
        ? next
        : { ...next, cacheRead: undefined, cacheWrite: undefined }
      const pricing = quickPriceValues(prices, target, quotaPerUnit)
      const updated = quickPrice(
        { ...target, effective: { ...target.effective, ...pricing } },
        quotaPerUnit
      )
      props.onPatch(
        props.row.source_name,
        {
          quickPricing: {
            ...next,
            cacheRead: updateCache ? next.cacheRead : updated?.cacheRead,
            cacheWrite: updateCache ? next.cacheWrite : updated?.cacheWrite,
            longCacheWrite: updated?.longCacheWrite,
          },
          priceError: undefined,
          pricing,
          pricingVersion,
          pricingBase: undefined,
          previewPricing: undefined,
        },
        expectedQuickPricing
      )
    } catch (error) {
      let message =
        error instanceof Error ? error.message : t('Invalid pricing value')
      if (message === t('Enter a finite, non-negative price')) {
        message = t('Enter a price of 0 or more. Decimals are supported.')
      }
      props.onPatch(
        props.row.source_name,
        {
          quickPricing: next,
          priceError: message,
          pricingBase: entry,
          pricingVersion,
        },
        expectedQuickPricing
      )
    }
  }
  const message =
    props.row.priceError ||
    conversion.error?.message ||
    conversion.data?.unsupported_reason
  return (
    <Tooltip disabled={!message || Boolean(props.row.priceError)}>
      <TooltipTrigger
        render={
          <div
            className='batch-inline-pricing'
            title={props.row.priceError}
            onFocusCapture={() => setPrepareConversion(true)}
            onMouseEnter={() => setPrepareConversion(true)}
          />
        }
      >
        <div className='batch-price-controls' data-mode={value?.mode}>
          <Combobox
            className='batch-mode'
            popupClassName='w-52 min-w-52 data-[chips=true]:min-w-52 max-w-[90vw]'
            aria-label={t('Billing mode for {{model}}', {
              model: props.row.source_name,
            })}
            value={value?.mode ?? 'advanced'}
            disabled={waitingForPrice}
            options={[
              { value: 'token', label: t('Quick token pricing') },
              { value: 'request', label: t('Quick request pricing') },
              { value: 'advanced', label: t('Tiered / advanced') },
            ]}
            onValueChange={(mode) => {
              if (mode === 'advanced' || !value) {
                props.onEdit()
                return
              }
              void change({
                mode: mode === 'request' ? 'request' : 'token',
                input: '',
                output: '',
              })
            }}
          />
          {value && (
            <QuickPriceFields
              name={props.row.source_name}
              value={value}
              onChange={(next) => void change(next)}
              disabled={waitingForPrice}
              errorId={props.row.priceError ? errorId : undefined}
            />
          )}
          {value?.mode === 'token' && (
            <CachePriceFields
              compact
              errorId={props.row.priceError ? errorId : undefined}
              value={value}
              name={props.row.source_name}
              onChange={(next) => void change(next, true)}
              disabled={
                waitingForPrice ||
                (legacyPricing && !conversion.data?.expression)
              }
            />
          )}
        </div>
        {message && (
          <span id={errorId} role='alert' className='sr-only'>
            {message}
          </span>
        )}
      </TooltipTrigger>
      {message && !props.row.priceError && (
        <TooltipContent role='tooltip'>{message}</TooltipContent>
      )}
    </Tooltip>
  )
}
