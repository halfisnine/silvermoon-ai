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
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Checkbox } from '@/components/ui/checkbox'
import { Field, FieldLabel } from '@/components/ui/field'

import { validateModelMappingJson, type ModelMappingPair } from '../lib'

export function ModelMappingPreview(props: {
  pairs: ModelMappingPair[]
  existing?: Record<string, string>
}) {
  const { t } = useTranslation()
  return (
    <div className='min-w-0 space-y-3'>
      <div className='text-muted-foreground grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] gap-2 text-xs'>
        <span>{t('Users call')}</span>
        <span aria-hidden='true'>→</span>
        <span>{t('Upstream receives')}</span>
      </div>
      <ul
        aria-label={t('Preview')}
        className='flex max-h-56 min-w-0 flex-col gap-3 overflow-y-auto text-xs'
      >
        {props.pairs.map((pair) => {
          const previous =
            props.existing && Object.hasOwn(props.existing, pair.from)
              ? props.existing[pair.from]
              : undefined
          return (
            <li key={pair.from} className='space-y-1'>
              <div className='grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-start gap-2 font-mono'>
                <span className='min-w-0 wrap-anywhere'>{pair.from}</span>
                <span aria-hidden='true'>→</span>
                <span className='min-w-0 wrap-anywhere'>{pair.to}</span>
              </div>
              {props.existing && (
                <div className='text-muted-foreground min-w-0 wrap-anywhere'>
                  {previous === undefined && t('New mapping')}
                  {previous === pair.to && t('Already mapped')}
                  {previous !== undefined && previous !== pair.to && (
                    <>
                      <span className='text-warning font-medium'>
                        {t('Will overwrite')}
                      </span>
                      {' · '}
                      <span className='font-mono'>{previous}</span>
                    </>
                  )}
                </div>
              )}
            </li>
          )
        })}
      </ul>
    </div>
  )
}

export function ModelMappingConfirmation(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  pairs: ModelMappingPair[]
  currentMapping: string
  showSync: boolean
  syncModels: boolean
  onSyncChange: (value: boolean) => void
  onConfirm: () => void
  failed: boolean
}) {
  const { t } = useTranslation()
  const id = useId()
  const validation = validateModelMappingJson(props.currentMapping)
  const existing: Record<string, string> | undefined = validation.valid
    ? JSON.parse(props.currentMapping.trim() || '{}')
    : undefined
  return (
    <ConfirmDialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('Confirm mappings')}
      desc={t('Save the channel after adding mappings to apply these changes.')}
      cancelBtnText={t('Back to editing')}
      confirmText={t('Add {{count}} mapping(s) and close', {
        count: props.pairs.length,
      })}
      disabled={!validation.valid || props.pairs.length === 0}
      handleConfirm={props.onConfirm}
      className='w-[calc(100%-2rem)] data-[size=default]:max-w-2xl data-[size=default]:sm:max-w-2xl [&_[data-slot=alert-dialog-footer]]:flex-wrap [&_button]:h-auto [&_button]:min-h-9 [&_button]:whitespace-normal'
    >
      <ModelMappingPreview pairs={props.pairs} existing={existing} />
      {!validation.valid && (
        <p role='alert' className='text-destructive text-sm'>
          {t(validation.error ?? 'Model mapping must be valid JSON format')}
        </p>
      )}
      {props.failed && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Could not add mappings. Return to editing and try again.')}
        </p>
      )}
      {props.showSync && (
        <Field orientation='horizontal'>
          <Checkbox
            id={id}
            checked={props.syncModels}
            onCheckedChange={(checked) => props.onSyncChange(checked === true)}
          />
          <FieldLabel htmlFor={id} className='leading-5 font-normal'>
            {t(
              'Publish the request names and remove the raw upstream names from the model list'
            )}
          </FieldLabel>
        </Field>
      )}
    </ConfirmDialog>
  )
}
