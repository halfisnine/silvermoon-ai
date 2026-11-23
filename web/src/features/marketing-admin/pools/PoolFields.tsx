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
  useFieldArray,
  useFormContext,
  useWatch,
  type FieldPath,
} from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { DateTimePicker } from '@/components/datetime-picker'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import { newGift, type PoolFormValues } from './lib/schema'

export function PoolInput(props: {
  name: FieldPath<PoolFormValues>
  label: string
  numeric?: boolean
  decimal?: boolean
  disabled?: boolean
}) {
  const form = useFormContext<PoolFormValues>()
  return (
    <FormField
      control={form.control}
      name={props.name}
      render={({ field }) => (
        <FormItem>
          <FormLabel>{props.label}</FormLabel>
          <FormControl>
            <Input
              {...field}
              value={
                typeof field.value === 'string' ||
                (typeof field.value === 'number' &&
                  Number.isFinite(field.value))
                  ? field.value
                  : ''
              }
              disabled={props.disabled}
              type={props.numeric ? 'number' : 'text'}
              inputMode={props.numeric || props.decimal ? 'decimal' : 'text'}
              step={props.decimal ? 'any' : 1}
              onChange={(event) =>
                field.onChange(
                  props.numeric
                    ? event.target.valueAsNumber
                    : event.target.value
                )
              }
            />
          </FormControl>
          <FormMessage />
        </FormItem>
      )}
    />
  )
}
export function PoolDate(props: {
  name: 'starts_at' | 'ends_at'
  label: string
  disabled?: boolean
}) {
  const form = useFormContext<PoolFormValues>()
  return (
    <FormField
      control={form.control}
      name={props.name}
      render={({ field }) => (
        <FormItem>
          <FormLabel>{props.label}</FormLabel>
          <FormControl>
            <DateTimePicker
              timeZone='Asia/Shanghai'
              value={field.value ? new Date(field.value * 1000) : undefined}
              onChange={(date) =>
                field.onChange(date ? Math.floor(date.getTime() / 1000) : 0)
              }
              disabled={props.disabled}
            />
          </FormControl>
          <FormMessage />
        </FormItem>
      )}
    />
  )
}
export function PoolGiftRows(props: {
  prizeIndex: number
  currency: string
  disabled: boolean
}) {
  const { t } = useTranslation()
  const form = useFormContext<PoolFormValues>()
  const path = `prizes.${props.prizeIndex}.gifts` as const
  const gifts = useFieldArray({ control: form.control, name: path })
  return (
    <div className='space-y-3'>
      <p className='text-muted-foreground text-sm'>
        {t('All gift items in this prize are awarded together.')}
      </p>
      {gifts.fields.map((gift, index) => (
        <GiftRow
          key={gift.id}
          prizeIndex={props.prizeIndex}
          giftIndex={index}
          currency={props.currency}
          disabled={props.disabled}
          removable={gifts.fields.length > 1}
          onRemove={() => gifts.remove(index)}
        />
      ))}
      <Button
        type='button'
        variant='outline'
        disabled={props.disabled || gifts.fields.length >= 20}
        onClick={() => gifts.append(newGift())}
      >
        {t('Add combination gift')}
      </Button>
    </div>
  )
}
function GiftRow(props: {
  prizeIndex: number
  giftIndex: number
  currency: string
  disabled: boolean
  removable: boolean
  onRemove: () => void
}) {
  const { t } = useTranslation()
  const form = useFormContext<PoolFormValues>()
  const path = `prizes.${props.prizeIndex}.gifts.${props.giftIndex}` as const
  const permanent = useWatch({
    control: form.control,
    name: `${path}.permanent`,
  })
  return (
    <div className='grid items-start gap-3 sm:grid-cols-2 lg:grid-cols-5'>
      <PoolInput
        name={`${path}.amount`}
        label={t('Gift amount ({{currency}})', { currency: props.currency })}
        decimal
        disabled={props.disabled}
      />
      <FormField
        control={form.control}
        name={`${path}.permanent`}
        render={({ field }) => (
          <FormItem className='flex min-h-14 items-center gap-2'>
            <FormControl>
              <Checkbox
                checked={field.value}
                onCheckedChange={(checked) => {
                  field.onChange(checked)
                  if (checked) {
                    form.setValue(`${path}.duration`, 0)
                    form.clearErrors(`${path}.duration`)
                  }
                }}
                disabled={props.disabled}
              />
            </FormControl>
            <FormLabel>{t('Permanent gift')}</FormLabel>
            <FormMessage />
          </FormItem>
        )}
      />
      {!permanent && (
        <>
          <PoolInput
            name={`${path}.duration`}
            label={t('Validity period')}
            numeric
            decimal
            disabled={props.disabled}
          />
          <FormField
            control={form.control}
            name={`${path}.unit`}
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Duration unit')}</FormLabel>
                <FormControl>
                  <NativeSelect
                    {...field}
                    disabled={props.disabled}
                    className='w-full'
                  >
                    <NativeSelectOption value='days'>
                      {t('Days')}
                    </NativeSelectOption>
                    <NativeSelectOption value='hours'>
                      {t('Hours')}
                    </NativeSelectOption>
                    <NativeSelectOption value='minutes'>
                      {t('Minutes')}
                    </NativeSelectOption>
                  </NativeSelect>
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        </>
      )}
      {permanent && (
        <p className='text-muted-foreground pt-6 text-sm lg:col-span-2'>
          {t('Never expires')}
        </p>
      )}
      <Button
        type='button'
        variant='ghost'
        className='self-end'
        disabled={props.disabled || !props.removable}
        aria-label={t('Remove gift {{number}} from prize {{prize}}', {
          number: props.giftIndex + 1,
          prize: props.prizeIndex + 1,
        })}
        onClick={props.onRemove}
      >
        {t('Remove gift')}
      </Button>
    </div>
  )
}
