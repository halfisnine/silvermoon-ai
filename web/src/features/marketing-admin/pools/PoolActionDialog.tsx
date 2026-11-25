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
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { DateTimePicker } from '@/components/datetime-picker'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { formatMarketingDate } from '../format'
import { mutatePool, poolQueryKey } from './api'
import { usePoolOperationKey } from './lib/operation-key'
import type { PoolAction, PoolRecord } from './types'

export function PoolActionDialog(props: {
  record: PoolRecord
  action: PoolAction
  onClose: () => void
}) {
  const { t, i18n } = useTranslation()
  const language = i18n.resolvedLanguage || i18n.language
  const locale = toIntlLocale(language)
  const client = useQueryClient()
  const operationKey = usePoolOperationKey()
  const [pool] = useState(props.record)
  const finitePrizes = pool.pool.prizes.filter((prize) => prize.stock >= 0)
  const schema = z
    .object({
      amount: z.number({ error: t('Enter a valid number') }),
      prize_id: z.number({ error: t('Enter a valid number') }),
      ends_at: z.number({ error: t('Enter a valid number') }),
    })
    .superRefine((input, ctx) => {
      if (props.action === 'restock' || props.action === 'append') {
        if (!Number.isSafeInteger(input.amount) || input.amount <= 0) {
          ctx.addIssue({
            code: 'custom',
            path: ['amount'],
            message: t('Enter a positive whole number'),
          })
        }
        const previous =
          props.action === 'append'
            ? pool.pool.capacity
            : (finitePrizes.find((prize) => prize.id === input.prize_id)
                ?.stock ?? 0)
        if (!Number.isSafeInteger(previous + input.amount)) {
          ctx.addIssue({
            code: 'custom',
            path: ['amount'],
            message: t('The number is too large'),
          })
        }
      }
      if (
        props.action === 'restock' &&
        !finitePrizes.some((prize) => prize.id === input.prize_id)
      ) {
        ctx.addIssue({
          code: 'custom',
          path: ['prize_id'],
          message: t('Select a prize with limited stock'),
        })
      }
      if (
        props.action === 'append' &&
        pool.appendable_capacity !== null &&
        input.amount > pool.appendable_capacity
      ) {
        ctx.addIssue({
          code: 'custom',
          path: ['amount'],
          message: t('The requested increase exceeds available prize supply'),
        })
      }
      if (
        props.action === 'extend' &&
        (!Number.isSafeInteger(input.ends_at) ||
          input.ends_at <= pool.pool.ends_at)
      ) {
        ctx.addIssue({
          code: 'custom',
          path: ['ends_at'],
          message: t(
            'The new deadline must be later than the current deadline'
          ),
        })
      }
    })
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    defaultValues: {
      amount: 0,
      prize_id: finitePrizes[0]?.id ?? 0,
      ends_at: 0,
    },
  })
  const amount = form.watch('amount')
  const mutation = useMutation({
    mutationFn: (input: z.infer<typeof schema>) => {
      const payload = {
        revision: pool.revision,
        amount:
          props.action === 'append' || props.action === 'restock'
            ? input.amount
            : undefined,
        prize_id: props.action === 'restock' ? input.prize_id : undefined,
        ends_at: props.action === 'extend' ? input.ends_at : undefined,
      }
      return mutatePool(pool.id, props.action, {
        ...payload,
        operation_key: operationKey(payload),
      })
    },
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: poolQueryKey })
      toast.success(t('Prize pool updated'))
      props.onClose()
    },
    onError: (error) =>
      form.setError('root.server', { message: getServerErrorMessage(error) }),
  })
  let title = t('Pause prize pool')
  let description = t(
    'Pausing does not consume chances. Existing expiry times continue to apply.'
  )
  if (props.action === 'resume') {
    title = t('Resume prize pool')
    description = t(
      'Resume draws using the existing rules and remaining stock.'
    )
  }
  if (props.action === 'restock') {
    title = t('Restock prize')
    description = t(
      'Restocking adds prize supply. It does not increase the total chance limit.'
    )
  }
  if (props.action === 'append') {
    title = t('Increase chance limit')
    description = t(
      'New capacity serves future qualifying events. Previous capacity shortages are not automatically rewarded.'
    )
  }
  if (props.action === 'extend') {
    title = t('Extend draw deadline')
    description = t(
      'Only chances that are still valid are extended. Expired or cleared chances are not restored.'
    )
  }
  return (
    <ConfirmDialog
      open
      onOpenChange={(open) => {
        if (!open && !mutation.isPending) props.onClose()
      }}
      title={title}
      desc={description}
      confirmText={t('Confirm')}
      isLoading={mutation.isPending}
      handleConfirm={() =>
        void form.handleSubmit((input) => {
          form.clearErrors('root')
          mutation.mutate(input)
        })()
      }
    >
      <Form {...form}>
        <form
          onSubmit={form.handleSubmit((input) => mutation.mutate(input))}
          className='space-y-4'
        >
          <p className='font-medium break-words'>{pool.pool.name}</p>
          {props.action === 'restock' && (
            <FormField
              control={form.control}
              name='prize_id'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Prize')}</FormLabel>
                  <FormControl>
                    <NativeSelect
                      value={field.value}
                      onChange={(event) =>
                        field.onChange(Number(event.target.value))
                      }
                      disabled={mutation.isPending}
                      className='w-full'
                    >
                      {finitePrizes.map((prize) => (
                        <NativeSelectOption key={prize.id} value={prize.id}>
                          {prize.name}
                        </NativeSelectOption>
                      ))}
                    </NativeSelect>
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          )}
          {(props.action === 'append' || props.action === 'restock') && (
            <FormField
              control={form.control}
              name='amount'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>
                    {props.action === 'append'
                      ? t('Additional chances')
                      : t('Additional stock')}
                  </FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      value={Number.isFinite(field.value) ? field.value : ''}
                      type='number'
                      min={1}
                      step={1}
                      disabled={mutation.isPending}
                      onChange={(event) =>
                        field.onChange(event.target.valueAsNumber)
                      }
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          )}
          {props.action === 'append' && (
            <dl className='grid grid-cols-2 gap-2 text-sm'>
              <dt>{t('Current chance limit')}</dt>
              <dd>{formatNumber(pool.pool.capacity, locale)}</dd>
              <dt>{t('New chance limit')}</dt>
              <dd>
                {formatNumber(
                  pool.pool.capacity + (Number.isFinite(amount) ? amount : 0),
                  locale
                )}
              </dd>
              <dt>{t('Available increase')}</dt>
              <dd>
                {pool.appendable_capacity === null
                  ? t('Not limited by prize supply')
                  : formatNumber(pool.appendable_capacity, locale)}
              </dd>
            </dl>
          )}
          {props.action === 'extend' && (
            <>
              <p className='text-muted-foreground text-sm'>
                {t('Current deadline')}:{' '}
                {formatMarketingDate(pool.pool.ends_at, language)}
              </p>
              <FormField
                control={form.control}
                name='ends_at'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('New deadline')}</FormLabel>
                    <FormControl>
                      <DateTimePicker
                        timeZone='Asia/Shanghai'
                        value={
                          field.value ? new Date(field.value * 1000) : undefined
                        }
                        onChange={(date) =>
                          field.onChange(
                            date ? Math.floor(date.getTime() / 1000) : 0
                          )
                        }
                        disabled={mutation.isPending}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </>
          )}
          {form.formState.errors.root?.server?.message && (
            <p role='alert' className='text-destructive text-sm'>
              {form.formState.errors.root.server.message}
            </p>
          )}
        </form>
      </Form>
    </ConfirmDialog>
  )
}
