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
import { useState } from 'react'
import { useFormContext, type FieldPath } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { DateTimePicker } from '@/components/datetime-picker'
import { Checkbox } from '@/components/ui/checkbox'
import {
  FormField,
  FormItem,
  FormLabel,
  FormControl,
  FormMessage,
  FormDescription,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import type { MarketingActivity } from '../types'

type Props = {
  name: FieldPath<MarketingActivity>
  label: string
  description?: string
  type?: 'number' | 'text' | 'date' | 'check'
  options?: { value: string; label: string }[]
  onChange?: (value: string) => void
  disabled?: boolean
}
/** Activity-specific binding keeps every conditional control in the same validation form. */
export function ActivityField(props: Props) {
  const form = useFormContext<MarketingActivity>()
  return (
    <FormField
      control={form.control}
      name={props.name}
      render={({ field }) => {
        let control
        if (props.options) {
          control = (
            <NativeSelect
              value={String(field.value ?? '')}
              onChange={(e) =>
                props.onChange
                  ? props.onChange(e.target.value)
                  : field.onChange(
                      props.type === 'number'
                        ? Number(e.target.value)
                        : e.target.value
                    )
              }
              onBlur={field.onBlur}
              ref={field.ref}
              disabled={props.disabled}
              className='w-full'
            >
              {props.options.map((o) => (
                <NativeSelectOption key={o.value} value={o.value}>
                  {o.label}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          )
        } else if (props.type === 'date') {
          control = (
            <DateTimePicker
              timeZone='Asia/Shanghai'
              value={
                field.value ? new Date(Number(field.value) * 1000) : undefined
              }
              onChange={(d) =>
                field.onChange(d ? Math.floor(d.getTime() / 1000) : 0)
              }
              ref={field.ref}
              disabled={props.disabled}
            />
          )
        } else if (props.type === 'check') {
          control = (
            <Checkbox
              checked={Boolean(field.value)}
              onCheckedChange={field.onChange}
              disabled={props.disabled}
              ref={field.ref}
            />
          )
        } else {
          control = (
            <Input
              value={String(field.value ?? '')}
              type={props.type === 'number' ? 'number' : 'text'}
              inputMode={props.type === 'number' ? 'numeric' : undefined}
              onChange={(e) =>
                field.onChange(
                  props.type === 'number'
                    ? Number(e.target.value)
                    : e.target.value
                )
              }
              onBlur={field.onBlur}
              ref={field.ref}
              disabled={props.disabled}
            />
          )
        }
        return (
          <FormItem>
            <FormLabel>{props.label}</FormLabel>
            <FormControl>{control}</FormControl>
            {props.description && (
              <FormDescription>{props.description}</FormDescription>
            )}
            <FormMessage />
          </FormItem>
        )
      }}
    />
  )
}

export function GiftValidityField(props: {
  name: FieldPath<MarketingActivity>
}) {
  const { t } = useTranslation()
  const form = useFormContext<MarketingActivity>()
  const [timed, setTimed] = useState(
    () => Number(form.getValues(props.name) ?? 0) > 0
  )
  const [unit, setUnit] = useState(() => {
    const seconds = Number(form.getValues(props.name) ?? 0)
    if (seconds && seconds % 86400 === 0) return 86400
    if (seconds && seconds % 3600 === 0) return 3600
    return 60
  })
  return (
    <FormField
      control={form.control}
      name={props.name}
      render={({ field }) => (
        <FormItem>
          <FormLabel>{t('Gift validity')}</FormLabel>
          <FormControl>
            <NativeSelect
              value={timed ? 'timed' : 'permanent'}
              ref={field.ref}
              onChange={(e) => {
                setTimed(e.target.value === 'timed')
                field.onChange(e.target.value === 'permanent' ? 0 : unit)
              }}
            >
              <NativeSelectOption value='permanent'>
                {t('Permanent gift (no expiry)')}
              </NativeSelectOption>
              <NativeSelectOption value='timed'>
                {t('Timed gift')}
              </NativeSelectOption>
            </NativeSelect>
          </FormControl>
          {timed && (
            <div className='flex gap-2'>
              <Input
                type='number'
                min={0}
                step='any'
                aria-label={t('Validity duration')}
                className='min-w-0 flex-1'
                value={
                  Number.isFinite(Number(field.value))
                    ? Number(field.value) / unit
                    : ''
                }
                onChange={(e) =>
                  field.onChange(
                    Number(e.target.value) > 0
                      ? Number(e.target.value) * unit
                      : Number.NaN
                  )
                }
                onBlur={field.onBlur}
              />
              <NativeSelect
                aria-label={t('Validity unit')}
                className='w-max min-w-24 shrink-0'
                value={unit}
                onChange={(e) => setUnit(Number(e.target.value))}
              >
                <NativeSelectOption value={86400}>
                  {t('Days')}
                </NativeSelectOption>
                <NativeSelectOption value={3600}>
                  {t('Hours')}
                </NativeSelectOption>
                <NativeSelectOption value={60}>
                  {t('Minutes')}
                </NativeSelectOption>
              </NativeSelect>
            </div>
          )}
          <FormDescription>
            {t(
              'Gifts are usable immediately. Timed validity starts when credited.'
            )}
          </FormDescription>
          <FormMessage />
        </FormItem>
      )}
    />
  )
}
