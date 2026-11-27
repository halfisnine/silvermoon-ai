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
import { Alert02Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import type { Dispatch, SetStateAction } from 'react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Combobox } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import type { ModelPricingConfig } from '@/features/model-pricing/api'
import { getLobeIcon } from '@/lib/lobe-icon'

import type { BatchSnapshot, DraftRow } from './api'
import { draftEntry, isDirty, renameDraft } from './draft'
import { InlinePricing } from './inline-pricing'

export function ModelTable(props: {
  rows: DraftRow[]
  snapshot: BatchSnapshot
  selected: Set<string>
  onSelectionChange: Dispatch<SetStateAction<Set<string>>>
  pricing: ModelPricingConfig
  busy: boolean
  onActiveRowChange: (source: string | null) => void
  onEdit: (row: DraftRow) => void
  onPatch: (
    source: string,
    values: Partial<DraftRow>,
    expectedQuickPricing?: DraftRow['quickPricing']
  ) => void
}) {
  const { t } = useTranslation()
  const allVisible =
    props.rows.length > 0 &&
    props.rows.every((row) => props.selected.has(row.source_name))
  const partiallyVisible = props.rows.some((row) =>
    props.selected.has(row.source_name)
  )
  return (
    <StaticDataTable
      className='batch-table'
      containerProps={{
        onFocusCapture: (event) =>
          props.onActiveRowChange(
            (event.target as HTMLElement).closest<HTMLElement>(
              '[data-model-source]'
            )?.dataset.modelSource ?? null
          ),
        onBlurCapture: (event) => {
          if (
            !event.currentTarget.contains(event.relatedTarget as Node | null)
          ) {
            props.onActiveRowChange(
              (event.relatedTarget as HTMLElement | null)?.closest<HTMLElement>(
                '[data-model-source]'
              )?.dataset.modelSource ?? null
            )
          }
        },
      }}
      tableClassName='min-w-[1180px] table-fixed [&_td]:whitespace-normal [&_th]:whitespace-normal'
      data={props.rows}
      getRowKey={(row) => row.source_name}
      emptyContent={<EmptyState title={t('No models found')} />}
      columns={[
        {
          id: 'select',
          header: (
            <Checkbox
              aria-label={t('Select visible models')}
              checked={allVisible}
              indeterminate={partiallyVisible && !allVisible}
              onCheckedChange={(checked) =>
                props.onSelectionChange((current) => {
                  const next = new Set(current)
                  for (const row of props.rows) {
                    if (checked) next.add(row.source_name)
                    else next.delete(row.source_name)
                  }
                  return next
                })
              }
            />
          ),
          className: 'w-9',
          cell: (row) => (
            <Checkbox
              aria-label={t('Select {{model}}', { model: row.source_name })}
              checked={props.selected.has(row.source_name)}
              onCheckedChange={(checked) =>
                props.onSelectionChange((current) => {
                  const next = new Set(current)
                  if (checked) next.add(row.source_name)
                  else next.delete(row.source_name)
                  return next
                })
              }
            />
          ),
        },
        {
          id: 'model',
          header: t('User model name'),
          cell: (row) => (
            <div
              className='batch-model-cell'
              data-model-source={row.source_name}
            >
              <Input
                title={row.name}
                aria-label={t('User model name for {{model}}', {
                  model: row.source_name,
                })}
                aria-invalid={
                  !row.name ||
                  /[\s,\p{Cc}]/u.test(row.name) ||
                  [...row.name].length > 128 ||
                  props.snapshot.rows.some(
                    (other) =>
                      other.source_name !== row.source_name &&
                      other.source_name === row.name
                  ) ||
                  props.rows.some(
                    (other) =>
                      other.source_name !== row.source_name &&
                      other.name === row.name
                  )
                }
                value={row.name}
                onChange={(event) => {
                  const name = event.target.value
                  props.onPatch(
                    row.source_name,
                    renameDraft(row, name, props.snapshot, props.pricing)
                  )
                }}
              />
            </div>
          ),
        },
        {
          id: 'vendor',
          header: t('Vendor'),
          className: 'w-[120px]',
          cell: (row) => (
            <div data-model-source={row.source_name}>
              <Combobox
                className='batch-vendor-select'
                popupClassName='w-60 min-w-60 data-[chips=true]:min-w-60 max-w-[90vw] [&_[role=option]]:whitespace-nowrap'
                aria-label={t('Vendor for {{model}}', {
                  model: row.source_name,
                })}
                value={String(row.vendorId)}
                onValueChange={(value) =>
                  props.onPatch(row.source_name, {
                    vendorId: Number(value),
                    vendorTouched: true,
                  })
                }
                placeholder={t('Select a saved vendor.')}
                searchPlaceholder={t('Search vendors')}
                options={[
                  { value: '0', label: t('Unassigned') },
                  ...props.snapshot.vendors.map((vendor) => ({
                    value: String(vendor.id),
                    label: vendor.name,
                    icon: getLobeIcon(vendor.icon, 20),
                  })),
                ]}
              />
            </div>
          ),
        },
        {
          id: 'pricing',
          header: `${t('Customer pricing')} (USD)`,
          className: 'w-[618px]',
          cell: (row) => (
            <div data-model-source={row.source_name}>
              <InlinePricing
                row={row}
                pricing={props.pricing}
                busy={props.busy}
                onPatch={props.onPatch}
                onEdit={() => props.onEdit(row)}
              />
            </div>
          ),
        },
        {
          id: 'upstream',
          header: t('Upstream model'),
          cell: (row) => (
            <span className='batch-upstream' title={row.upstream_name}>
              {row.upstream_name}
            </span>
          ),
        },
        {
          id: 'status',
          header: t('Status'),
          className: 'w-[72px]',
          cell: (row) => {
            if (row.priceError) {
              return (
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <span
                        className='batch-price-warning'
                        tabIndex={0}
                        role='img'
                        aria-label={t('Invalid pricing value')}
                      />
                    }
                  >
                    <HugeiconsIcon
                      icon={Alert02Icon}
                      size={14}
                      aria-hidden='true'
                    />
                    <span aria-hidden='true'>{t('Price')}</span>
                  </TooltipTrigger>
                  <TooltipContent role='tooltip'>
                    {row.priceError}
                  </TooltipContent>
                </Tooltip>
              )
            }
            return (
              <span
                className={
                  row.vendorId &&
                  !row.priceError &&
                  Object.keys(draftEntry(row, props.pricing).effective).some(
                    (key) =>
                      [
                        'ModelPrice',
                        'ModelRatio',
                        'billing_setting.billing_expr',
                      ].includes(key)
                  )
                    ? 'batch-status ready'
                    : 'batch-status todo'
                }
              >
                {row.vendorId &&
                !row.priceError &&
                Object.keys(draftEntry(row, props.pricing).effective).some(
                  (key) =>
                    [
                      'ModelPrice',
                      'ModelRatio',
                      'billing_setting.billing_expr',
                    ].includes(key)
                )
                  ? t('Configured')
                  : t('Incomplete')}
                {isDirty(row) && ' •'}
              </span>
            )
          },
        },
        {
          id: 'actions',
          header: t('Actions'),
          className: 'w-[80px]',
          cell: (row) => (
            <Button
              className='batch-advanced'
              size='sm'
              variant='link'
              disabled={
                props.busy &&
                !props.pricing.entries.some(
                  (entry) => entry.model_name === row.name
                )
              }
              onClick={() => props.onEdit(row)}
            >
              {t('Advanced settings')}
            </Button>
          ),
        },
      ]}
    />
  )
}
