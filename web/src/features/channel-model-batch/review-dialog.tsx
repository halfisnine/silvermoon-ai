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
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import type { ModelPricingConfig } from '@/features/model-pricing/api'
import type { PricingValues } from '@/features/model-pricing/pricing'

import type { BatchSnapshot, DraftRow } from './api'
import { draftEntry, resolveMetadata } from './draft'
import { parseEndpointConfig } from './endpoints'
import { PricingSummary } from './pricing-summary'

export function ReviewDialog(props: {
  snapshot: BatchSnapshot
  rows: DraftRow[]
  pricing: ModelPricingConfig
  effective: Record<string, PricingValues>
  isSaving: boolean
  error?: string
  onClose: () => void
  onSave: () => void
  onReload: () => void
}) {
  const { t } = useTranslation()
  return (
    <Dialog
      open
      title={t('Review changes')}
      description={t(
        'Review shared model settings and prices before saving. Models marked as shown may appear in the model square when a channel is available.'
      )}
      onOpenChange={(open) => {
        if (!open && !props.isSaving) props.onClose()
      }}
      contentClassName='sm:max-w-5xl'
      footer={
        <>
          <Button
            variant='outline'
            disabled={props.isSaving}
            onClick={() => props.onClose()}
          >
            {t('Back')}
          </Button>
          <Button
            disabled={props.isSaving || Boolean(props.error)}
            onClick={() => props.onSave()}
          >
            {props.isSaving ? t('Saving...') : t('Save changes')}
          </Button>
        </>
      }
    >
      <div className='space-y-4'>
        {props.rows.map((row) => {
          const beforeMetadata = resolveMetadata(
            props.snapshot.metadata,
            row.name
          )
          const before = props.pricing.entries.find(
            (entry) => entry.model_name === row.name
          )
          const other = props.snapshot.channels.filter(
            (channel) =>
              channel.id !== props.snapshot.channel.id &&
              channel.models.split(',').includes(row.name)
          )
          return (
            <section
              key={row.source_name}
              className='space-y-3 rounded-lg border p-3'
            >
              <p className='font-medium break-all'>
                {row.source_name} → {row.name} → {row.upstream_name}
              </p>
              <p className='text-sm'>
                {t('Vendor')}:{' '}
                {props.snapshot.vendors.find(
                  (vendor) => vendor.id === (beforeMetadata?.vendor_id ?? 0)
                )?.name ?? t('Unassigned')}{' '}
                →{' '}
                {props.snapshot.vendors.find(
                  (vendor) => vendor.id === row.vendorId
                )?.name ?? t('Unassigned')}{' '}
                · {t('Model icons inherit the selected vendor icon.')}
              </p>
              <p className='text-muted-foreground text-xs break-words'>
                {t('Square display')}:{' '}
                {beforeMetadata?.status === 1 ? t('Shown') : t('Hidden')}
                {' → '}
                {row.squareVisible ? t('Shown') : t('Hidden')}
              </p>
              <p className='text-muted-foreground text-xs break-words'>
                {t('Endpoint selection')}:{' '}
                {Object.keys(
                  parseEndpointConfig(beforeMetadata?.endpoints ?? '')
                ).join(', ') || t('Automatic by channel')}
                {' → '}
                {Object.keys(parseEndpointConfig(row.endpoints ?? '')).join(
                  ', '
                ) || t('Automatic by channel')}
              </p>
              <p className='text-muted-foreground text-xs break-words'>
                {t('Other channels using this customer name')}:{' '}
                {other.map((channel) => channel.name).join(', ') || t('None')}
              </p>
              {row.pricing ? (
                <div className='grid gap-4 sm:grid-cols-2'>
                  <div>
                    <h4 className='mb-2 text-sm'>{t('Before')}</h4>
                    <PricingSummary
                      entry={
                        before ?? {
                          model_name: row.name,
                          version: '',
                          configured: {},
                          effective: {},
                        }
                      }
                    />
                  </div>
                  <div>
                    <h4 className='mb-2 text-sm'>{t('After')}</h4>
                    <PricingSummary
                      entry={{
                        ...draftEntry(row, props.pricing),
                        effective:
                          props.effective[row.source_name] ?? row.pricing,
                      }}
                    />
                  </div>
                </div>
              ) : (
                <p className='text-muted-foreground text-sm'>
                  {t('Keep existing prices')}
                </p>
              )}
            </section>
          )
        })}
        {Boolean(props.error) && (
          <p role='alert' className='text-destructive'>
            {props.error}{' '}
            {t(
              'Your draft is retained. Reload and review current data before trying again.'
            )}{' '}
            <Button
              variant='outline'
              onClick={() => {
                props.onClose()
                props.onReload()
              }}
            >
              {t('Reload')}
            </Button>
          </p>
        )}
      </div>
    </Dialog>
  )
}
