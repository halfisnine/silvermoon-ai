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
import { isAxiosError } from 'axios'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
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
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'

import { marketingWrite } from '../api'
import { formatMarketingDuration, formatMarketingMoney } from '../format'
import { giftIssueSchema, giftIssueTotal, type GiftIssueValues } from './schema'
import type { GiftBatch, GiftBatchInput, GiftUser } from './types'
import { GiftUserPicker } from './user-picker'

export function GiftIssueDialog(props: {
  currency: string
  moneyRevision: string
  onClose: () => void
  onIssued: (batch: GiftBatch) => void
}) {
  const { t, i18n } = useTranslation()
  const client = useQueryClient()
  const [users, setUsers] = useState<GiftUser[]>([])
  const [confirmation, setConfirmation] = useState<GiftBatchInput>()
  const [attempt, setAttempt] = useState<GiftBatchInput>()
  const form = useForm<GiftIssueValues>({
    resolver: zodResolver(giftIssueSchema),
    defaultValues: {
      user_ids: [],
      amount: '',
      permanent: true,
      duration: 7,
      unit: 'days',
      reason: '',
    },
  })
  const values = form.watch()
  const mutation = useMutation({
    mutationFn: (input: GiftBatchInput) =>
      marketingWrite<GiftBatch>('/gift-batches', input),
    onSuccess: (batch) => {
      void client.invalidateQueries({ queryKey: ['marketing-admin'] })
      void client.invalidateQueries({ queryKey: ['marketing-user'] })
      props.onIssued(batch)
    },
    onError: (error) => {
      handleServerError(error)
      if (
        isAxiosError(error) &&
        [400, 409].includes(error.response?.status ?? 0)
      ) {
        setAttempt(undefined)
        setConfirmation(undefined)
        void client.invalidateQueries({
          queryKey: ['marketing-admin', 'metadata'],
        })
      }
    },
  })
  const locked = !!attempt || mutation.isPending
  const money = (amount: string) =>
    formatMarketingMoney(amount, props.currency, i18n.language)
  const prepare = (v: GiftIssueValues) =>
    setConfirmation({
      operation_key: crypto.randomUUID(),
      money_revision: props.moneyRevision,
      user_ids: [...v.user_ids],
      amount: v.amount,
      currency: props.currency,
      valid_seconds: v.permanent
        ? 0
        : v.duration * (v.unit === 'days' ? 86400 : 3600),
      reason: v.reason,
    })
  return (
    <>
      <Dialog
        open
        onOpenChange={(open) => {
          if (!open && !mutation.isPending && !confirmation) props.onClose()
        }}
        title={t('Issue gift credits')}
        description={t(
          'Gift credits are immediately available, non-refundable, and do not count as a recharge.'
        )}
        footer={
          <>
            <Button
              variant='outline'
              disabled={mutation.isPending}
              onClick={props.onClose}
            >
              {t('Close')}
            </Button>
            {attempt && mutation.isError ? (
              <Button onClick={() => mutation.mutate(attempt)}>
                {t('Retry this issuance')}
              </Button>
            ) : (
              <Button type='submit' form='gift-issue-form' disabled={locked}>
                {t('Review issuance')}
              </Button>
            )}
          </>
        }
      >
        <Form {...form}>
          <form
            id='gift-issue-form'
            onSubmit={form.handleSubmit(prepare)}
            className='flex flex-col gap-4'
          >
            <FormField
              control={form.control}
              name='user_ids'
              render={() => (
                <FormItem>
                  <FormLabel htmlFor='gift-user-search'>
                    {t('Recipients')}
                  </FormLabel>
                  <GiftUserPicker
                    users={users}
                    onChange={(next) => {
                      setUsers(next)
                      form.setValue(
                        'user_ids',
                        next.map((u) => u.id),
                        { shouldValidate: true }
                      )
                    }}
                    disabled={locked}
                  />
                  <FormMessage />
                </FormItem>
              )}
            />
            <div className='grid gap-4 sm:grid-cols-2'>
              <FormField
                control={form.control}
                name='amount'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>
                      {t('Gift amount per user')} ({props.currency})
                    </FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        inputMode='decimal'
                        maxLength={29}
                        disabled={locked}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <div
                className='bg-muted/40 flex items-center justify-between gap-3 overflow-x-auto rounded-lg p-3 text-sm whitespace-nowrap'
                aria-live='polite'
              >
                <span>
                  {t('Selected users: {{count}}', { count: users.length })}
                </span>
                <strong>
                  {t('Total gift amount')}:{' '}
                  {money(giftIssueTotal(values.amount, users.length))}
                </strong>
              </div>
            </div>
            <div className='flex items-center gap-3'>
              <FormField
                control={form.control}
                name='permanent'
                render={({ field }) => (
                  <FormItem className='flex min-h-8 min-w-0 items-center gap-2'>
                    <FormControl>
                      <Checkbox
                        checked={field.value}
                        onCheckedChange={field.onChange}
                        disabled={locked}
                      />
                    </FormControl>
                    <FormLabel className='text-xs sm:text-sm sm:whitespace-nowrap'>
                      {t('Permanent gift (no expiry)')}
                    </FormLabel>
                  </FormItem>
                )}
              />
              {!values.permanent && (
                <>
                  <FormField
                    control={form.control}
                    name='duration'
                    render={({ field }) => (
                      <FormItem className='min-w-0'>
                        <div className='flex items-center gap-2'>
                          <FormLabel className='sr-only shrink-0 sm:not-sr-only sm:whitespace-nowrap'>
                            {t('Validity after credit')}
                          </FormLabel>
                          <FormControl>
                            <Input
                              {...field}
                              value={
                                Number.isFinite(field.value) ? field.value : ''
                              }
                              type='number'
                              step='any'
                              className='w-16 sm:w-20'
                              disabled={locked}
                              onChange={(e) =>
                                field.onChange(e.target.valueAsNumber)
                              }
                            />
                          </FormControl>
                        </div>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <FormField
                    control={form.control}
                    name='unit'
                    render={({ field }) => (
                      <FormItem className='shrink-0'>
                        <FormLabel className='sr-only'>{t('Unit')}</FormLabel>
                        <FormControl>
                          <NativeSelect
                            {...field}
                            disabled={locked}
                            className='w-20'
                          >
                            <NativeSelectOption value='days'>
                              {t('Days')}
                            </NativeSelectOption>
                            <NativeSelectOption value='hours'>
                              {t('Hours')}
                            </NativeSelectOption>
                          </NativeSelect>
                        </FormControl>
                      </FormItem>
                    )}
                  />
                </>
              )}
            </div>
            <FormField
              control={form.control}
              name='reason'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Internal issuance reason')}</FormLabel>
                  <FormControl>
                    <Textarea {...field} maxLength={500} disabled={locked} />
                  </FormControl>
                  <p className='text-muted-foreground text-xs'>
                    {t('Visible to administrators only.')}
                  </p>
                  <FormMessage />
                </FormItem>
              )}
            />
            {attempt && mutation.isError && (
              <p role='alert' className='text-destructive text-sm'>
                {t(
                  'The issuance result is unconfirmed. Retry this issuance or check the batch records before issuing again.'
                )}
              </p>
            )}
          </form>
        </Form>
      </Dialog>
      <ConfirmDialog
        open={!!confirmation}
        onOpenChange={(open) => {
          if (!open && !mutation.isPending) setConfirmation(undefined)
        }}
        title={t('Confirm gift issuance')}
        confirmText={t('Confirm issuance')}
        isLoading={mutation.isPending}
        desc={t(
          'Each selected user receives the amount below. Timed gifts start when each credit succeeds.'
        )}
        handleConfirm={() => {
          if (confirmation) {
            setAttempt(confirmation)
            mutation.mutate(confirmation)
          }
        }}
      >
        {confirmation && (
          <div className='flex max-h-80 flex-col gap-3 overflow-y-auto text-sm'>
            {attempt && mutation.isError && (
              <p role='alert' className='text-destructive'>
                {t(
                  'The issuance result is unconfirmed. Retry this issuance or check the batch records before issuing again.'
                )}
              </p>
            )}
            <p>
              {t('Gift amount per user')}:{' '}
              <strong>{money(confirmation.amount)}</strong>
            </p>
            <p>
              {t('Total gift amount')}:{' '}
              <strong>
                {money(
                  giftIssueTotal(
                    confirmation.amount,
                    confirmation.user_ids.length
                  )
                )}
              </strong>
            </p>
            <p>
              {t('Validity')}:{' '}
              {confirmation.valid_seconds
                ? formatMarketingDuration(
                    confirmation.valid_seconds,
                    i18n.language
                  )
                : t('Never expires')}
            </p>
            <p className='break-words'>
              {t('Internal issuance reason')}: {confirmation.reason}
            </p>
            <ul className='grid gap-1 sm:grid-cols-2'>
              {users.map((u) => (
                <li key={u.id} className='break-all'>
                  {u.username} · ID {u.id}
                </li>
              ))}
            </ul>
          </div>
        )}
      </ConfirmDialog>
    </>
  )
}
