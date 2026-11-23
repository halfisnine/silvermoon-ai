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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useFieldArray, useForm, useWatch } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { poolQueryKey, savePool } from './api'
import { usePoolOperationKey } from './lib/operation-key'
import {
  newPrize,
  poolFormDefaults,
  poolFormPayload,
  poolFormSchema,
  type PoolFormValues,
} from './lib/schema'
import { PoolDate, PoolGiftRows, PoolInput } from './PoolFields'
import type { PoolRecord } from './types'

export function PoolEditor(props: {
  record?: PoolRecord
  copy?: boolean
  currency: string
  moneyRevision: string
  onClose: () => void
}) {
  const { t, i18n } = useTranslation()
  const [snapshot] = useState(() => ({
    record: props.record,
    currency: props.currency,
    moneyRevision: props.moneyRevision,
  }))
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const formId = useId()
  const client = useQueryClient()
  const operationKey = usePoolOperationKey()
  const form = useForm<PoolFormValues>({
    resolver: zodResolver(poolFormSchema),
    defaultValues: poolFormDefaults(snapshot.record?.pool, props.copy),
  })
  const prizes = useFieldArray({ control: form.control, name: 'prizes' })
  const values = useWatch({ control: form.control })
  const locked = !!snapshot.record && !props.copy && !snapshot.record.editable
  const save = useMutation({
    mutationFn: async (input: PoolFormValues) => {
      if (
        snapshot.record &&
        !props.copy &&
        input.ends_at < snapshot.record.pool.ends_at
      ) {
        form.setError('ends_at', {
          message: t('The existing deadline cannot be shortened'),
        })
        throw new Error(t('The existing deadline cannot be shortened'))
      }
      const payload = {
        money_revision: snapshot.moneyRevision,
        pool: poolFormPayload(input, snapshot.currency),
        revision: props.copy ? undefined : snapshot.record?.revision,
      }
      return savePool(
        { ...payload, operation_key: operationKey(payload) },
        props.copy ? undefined : snapshot.record?.id
      )
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: poolQueryKey })
      toast.success(t('Prize pool saved'))
      props.onClose()
    },
    onError: (error) => {
      form.setError('root.server', { message: getServerErrorMessage(error) })
    },
  })
  const disabled = locked || save.isPending
  const weightTotal = (values.prizes ?? []).reduce(
    (sum, prize) =>
      sum + (Number.isFinite(prize.weight) ? (prize.weight ?? 0) : 0),
    0
  )
  let title = t('Create prize pool')
  if (snapshot.record) title = t('Edit prize pool')
  if (props.copy) title = t('Copy prize pool')
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !save.isPending) props.onClose()
      }}
      title={title}
      description={t(
        'Configure prizes and a finite chance limit. Gift rewards are credited automatically.'
      )}
      contentClassName='sm:max-w-5xl'
      contentHeight='min(70vh, 780px)'
      footer={
        <>
          <Button
            variant='outline'
            disabled={save.isPending}
            onClick={props.onClose}
          >
            {t('Cancel')}
          </Button>
          <Button type='submit' form={formId} disabled={disabled}>
            {save.isPending ? t('Saving...') : t('Save')}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id={formId}
          onSubmit={form.handleSubmit((input) => {
            form.clearErrors('root')
            save.mutate(input)
          })}
          className='space-y-5'
        >
          {props.copy && (
            <p className='text-muted-foreground text-sm'>
              {t(
                'Set a new name and dates, and review prize stock. Existing chances and rewards remain in the original pool.'
              )}
            </p>
          )}
          {locked && (
            <p role='status'>
              {t(
                'Core rules are locked. Copy this pool to change prizes or weights.'
              )}
            </p>
          )}
          <fieldset disabled={disabled} className='grid gap-4 sm:grid-cols-2'>
            <legend className='sr-only'>{t('Prize pool settings')}</legend>
            <PoolInput name='name' label={t('Prize pool name')} />
            <FormField
              control={form.control}
              name='style'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Draw appearance')}</FormLabel>
                  <FormControl>
                    <NativeSelect {...field} className='w-full'>
                      <NativeSelectOption value='wheel'>
                        {t('Prize wheel')}
                      </NativeSelectOption>
                      <NativeSelectOption value='blind_box'>
                        {t('Blind box')}
                      </NativeSelectOption>
                    </NativeSelect>
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <PoolDate
              name='starts_at'
              label={t('Draws open at')}
              disabled={disabled}
            />
            <PoolDate
              name='ends_at'
              label={t('Draws close at')}
              disabled={disabled}
            />
            <PoolInput
              name='capacity'
              label={t('Total chance limit')}
              numeric
            />
            <FormField
              control={form.control}
              name='allow_empty'
              render={({ field }) => (
                <FormItem className='flex items-center gap-2'>
                  <FormControl>
                    <Checkbox
                      checked={field.value}
                      disabled={disabled}
                      onCheckedChange={(checked) => {
                        field.onChange(checked)
                        if (checked) {
                          prizes.append({
                            name: t('No prize'),
                            weight: 1,
                            stock: -1,
                            gifts: [],
                          })
                        } else {
                          prizes.replace(
                            form
                              .getValues('prizes')
                              .filter((prize) => prize.gifts.length > 0)
                          )
                        }
                      }}
                    />
                  </FormControl>
                  <FormLabel>{t('Allow no-prize outcomes')}</FormLabel>
                  <FormMessage />
                </FormItem>
              )}
            />
          </fieldset>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Weights determine relative odds among available prizes. Empty prizes do not increase the chance supply.'
            )}
          </p>
          <FormField
            control={form.control}
            name='prizes'
            render={() => (
              <FormItem>
                <FormMessage />
              </FormItem>
            )}
          />
          {prizes.fields.map((prize, index) => {
            const current = values.prizes?.[index]
            const empty = current?.gifts?.length === 0
            const chance =
              weightTotal > 0 ? (100 * (current?.weight ?? 0)) / weightTotal : 0
            return (
              <fieldset
                key={prize.id}
                disabled={disabled}
                className='space-y-3 border-t pt-4'
              >
                <legend className='px-1 text-sm font-semibold'>
                  {t('Prize {{number}}', { number: index + 1 })}
                </legend>
                <div className='flex flex-wrap items-center justify-between gap-2'>
                  <span className='text-muted-foreground text-sm'>
                    {t(
                      'Weight share when all prizes are available: {{percent}}%',
                      { percent: formatNumber(chance, locale) }
                    )}
                  </span>
                  <div className='flex flex-wrap gap-1'>
                    <Button
                      type='button'
                      variant='ghost'
                      disabled={disabled || prizes.fields.length >= 100}
                      onClick={() =>
                        prizes.insert(
                          index + 1,
                          structuredClone(form.getValues(`prizes.${index}`))
                        )
                      }
                    >
                      {t('Duplicate prize')}
                    </Button>
                    <Button
                      type='button'
                      variant='ghost'
                      disabled={disabled || index === 0}
                      aria-label={t('Move prize {{number}} up', {
                        number: index + 1,
                      })}
                      onClick={() => prizes.move(index, index - 1)}
                    >
                      {t('Move up')}
                    </Button>
                    <Button
                      type='button'
                      variant='ghost'
                      disabled={disabled || index === prizes.fields.length - 1}
                      aria-label={t('Move prize {{number}} down', {
                        number: index + 1,
                      })}
                      onClick={() => prizes.move(index, index + 1)}
                    >
                      {t('Move down')}
                    </Button>
                    <Button
                      type='button'
                      variant='ghost'
                      disabled={disabled || prizes.fields.length === 1}
                      aria-label={t('Remove prize {{number}}', {
                        number: index + 1,
                      })}
                      onClick={() => prizes.remove(index)}
                    >
                      {t('Remove')}
                    </Button>
                  </div>
                </div>
                <div className='grid items-start gap-3 sm:grid-cols-2 lg:grid-cols-4'>
                  <PoolInput
                    name={`prizes.${index}.name`}
                    label={t('Prize name')}
                  />
                  <PoolInput
                    name={`prizes.${index}.weight`}
                    label={t('Weight')}
                    numeric
                  />
                  <FormField
                    control={form.control}
                    name={`prizes.${index}.stock`}
                    render={({ field }) => (
                      <FormItem className='flex min-h-14 items-center gap-2'>
                        <FormControl>
                          <Checkbox
                            checked={field.value === -1}
                            disabled={disabled}
                            onCheckedChange={(checked) =>
                              field.onChange(checked ? -1 : 0)
                            }
                          />
                        </FormControl>
                        <FormLabel>{t('Unlimited stock')}</FormLabel>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  {current?.stock !== -1 && (
                    <PoolInput
                      name={`prizes.${index}.stock`}
                      label={t('Prize stock')}
                      numeric
                    />
                  )}
                </div>
                {empty ? (
                  <p className='text-muted-foreground text-sm'>
                    {t(
                      'A no-prize result consumes one chance and does not credit a gift.'
                    )}
                  </p>
                ) : (
                  <PoolGiftRows
                    prizeIndex={index}
                    currency={snapshot.currency}
                    disabled={disabled}
                  />
                )}
              </fieldset>
            )
          })}
          <Button
            type='button'
            variant='outline'
            disabled={disabled || prizes.fields.length >= 100}
            onClick={() => prizes.append(newPrize())}
          >
            {t('Add prize')}
          </Button>
          {form.formState.errors.root?.server?.message && (
            <p role='alert' className='text-destructive text-sm'>
              {form.formState.errors.root.server.message}
            </p>
          )}
        </form>
      </Form>
    </Dialog>
  )
}
