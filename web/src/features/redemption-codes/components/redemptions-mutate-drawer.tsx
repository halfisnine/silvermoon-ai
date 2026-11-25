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
import { useMutation, useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { DateTimePicker } from '@/components/datetime-picker'
import {
  SideDrawerSection,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Textarea } from '@/components/ui/textarea'
import { handleServerError } from '@/lib/handle-server-error'
import {
  createServerError,
  getServerErrorMessage,
} from '@/lib/server-error-message'
import { addTimeToDate } from '@/lib/time'

import {
  createRedemption,
  updateRedemption,
  getRedemption,
  getRedemptionMoneyContext,
} from '../api'
import { SUCCESS_MESSAGES } from '../constants'
import {
  getRedemptionFormSchema,
  type RedemptionFormValues,
  REDEMPTION_FORM_DEFAULT_VALUES,
  transformFormDataToPayload,
  transformRedemptionToFormDefaults,
} from '../lib'
import { formatRedemptionMoney } from '../lib/display'
import type { Redemption } from '../types'
import {
  RedemptionsExportDialog,
  type RedemptionExportData,
} from './redemptions-export-dialog'
import { useRedemptions } from './redemptions-provider'

type RedemptionsMutateDrawerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow?: Redemption
}

export function RedemptionsMutateDrawer(props: RedemptionsMutateDrawerProps) {
  const { t, i18n } = useTranslation()
  const redemptionId = props.currentRow?.id
  const isUpdate = redemptionId !== undefined
  const { triggerRefresh } = useRedemptions()
  const [createdCodes, setCreatedCodes] = useState<RedemptionExportData | null>(
    null
  )
  const form = useForm<RedemptionFormValues>({
    resolver: zodResolver(getRedemptionFormSchema(t)),
    defaultValues: REDEMPTION_FORM_DEFAULT_VALUES,
  })
  const [snapshot, setSnapshot] = useState<{
    target: number | 'create'
    currency: string
    moneyRevision: string
  } | null>(null)
  const target = redemptionId ?? 'create'
  const detail = useQuery({
    queryKey: ['redemption-detail', redemptionId],
    enabled: props.open && isUpdate,
    retry: false,
    staleTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    meta: { errorToast: false },
    queryFn: async () => {
      if (redemptionId === undefined) throw new Error('Missing redemption code')
      const response = await getRedemption(redemptionId)
      if (
        !response.success ||
        !response.data ||
        response.data.id !== redemptionId
      ) {
        throw createServerError(response, t('Failed to load'))
      }
      return response.data
    },
  })
  const moneyContext = useQuery({
    queryKey: ['marketing-admin-metadata'],
    enabled: props.open && !isUpdate,
    retry: false,
    staleTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    meta: { errorToast: false },
    queryFn: async () => {
      const response = await getRedemptionMoneyContext()
      if (!response.success || !response.data) {
        throw createServerError(response, t('Failed to load'))
      }
      return response.data
    },
  })
  useEffect(() => {
    if (!props.open) {
      setSnapshot(null)
      form.reset(REDEMPTION_FORM_DEFAULT_VALUES)
      return
    }
    // Keep input amounts tied to the conversion shown when editing began.
    if (snapshot?.target === target) return
    if (isUpdate) {
      if (!detail.isSuccess || detail.isFetching || !detail.data) return
      setSnapshot({
        target,
        currency: detail.data.credit_currency,
        moneyRevision: detail.data.money_revision,
      })
      form.reset(transformRedemptionToFormDefaults(detail.data))
    } else {
      if (
        !moneyContext.isSuccess ||
        moneyContext.isFetching ||
        !moneyContext.data
      ) {
        return
      }
      setSnapshot({
        target,
        currency: moneyContext.data.currency,
        moneyRevision: moneyContext.data.money_revision,
      })
      form.reset(REDEMPTION_FORM_DEFAULT_VALUES)
    }
  }, [
    props.open,
    target,
    snapshot,
    isUpdate,
    detail.isSuccess,
    detail.isFetching,
    detail.data,
    moneyContext.isSuccess,
    moneyContext.isFetching,
    moneyContext.data,
    form,
  ])
  const currency = snapshot?.target === target ? snapshot.currency : ''
  const moneyRevision =
    snapshot?.target === target ? snapshot.moneyRevision : ''
  const ready =
    props.open &&
    snapshot?.target === target &&
    (isUpdate
      ? detail.isSuccess && !detail.isFetching && !detail.data?.redeemed
      : moneyContext.isSuccess && !moneyContext.isFetching)
  const save = useMutation({
    mutationFn: async (data: RedemptionFormValues) => {
      const payload = transformFormDataToPayload(data, currency, moneyRevision)
      const response = isUpdate
        ? await updateRedemption({ ...payload, id: redemptionId })
        : await createRedemption(payload)
      if (!response.success) throw createServerError(response)
      return { response, payload }
    },
    onSuccess: ({ response, payload }) => {
      toast.success(
        t(
          isUpdate
            ? SUCCESS_MESSAGES.REDEMPTION_UPDATED
            : SUCCESS_MESSAGES.REDEMPTION_CREATED
        )
      )
      if (!isUpdate && Array.isArray(response.data) && response.data.length) {
        setCreatedCodes({
          keys: response.data,
          name: payload.name,
          credit: formatRedemptionMoney(
            payload.credit_amount,
            payload.credit_currency,
            i18n.language
          ),
          paid: formatRedemptionMoney(
            payload.paid_amount_cny,
            'CNY',
            i18n.language
          ),
          salesChannel: payload.sales_channel,
          salesBatch: payload.sales_batch,
        })
      }
      props.onOpenChange(false)
      triggerRefresh()
    },
    onError: (error) => {
      form.setError('root.server', {
        message: getServerErrorMessage(
          error,
          t('Could not save. Review the details and try again.')
        ),
      })
      handleServerError(error)
    },
  })
  const submit = form.handleSubmit((data) => {
    if (!ready || save.isPending) return
    form.clearErrors('root')
    save.mutate(data)
  })
  const textFields = [
    { name: 'name', label: t('Name'), placeholder: t('Enter a name') },
    {
      name: 'paid_amount_cny',
      label: t('Actual payment (CNY)'),
      placeholder: t('Enter the actual sale price in CNY'),
    },
    {
      name: 'credit_amount',
      label: t('Credited paid amount ({{currency}})', { currency }),
      placeholder: t('Enter the amount credited to the wallet'),
    },
    {
      name: 'sales_channel',
      label: t('Sales channel'),
      placeholder: t('For example, your card shop'),
    },
    {
      name: 'sales_batch',
      label: t('Sales batch'),
      placeholder: t('A batch name for tracing sales'),
    },
  ] as const
  return (
    <>
      <Sheet
        open={props.open}
        onOpenChange={(open) => {
          if (!save.isPending) props.onOpenChange(open)
        }}
      >
        <SheetContent
          className={sideDrawerContentClassName('sm:max-w-[600px]')}
        >
          <SheetHeader className={sideDrawerHeaderClassName()}>
            <SheetTitle>
              {isUpdate
                ? t('Update Redemption Code')
                : t('Create Redemption Code')}
            </SheetTitle>
            <SheetDescription>
              {t(
                'Paid codes credit the wallet as a payment. Activity gifts are issued separately.'
              )}
            </SheetDescription>
          </SheetHeader>
          <Form {...form}>
            <form
              id='redemption-form'
              onSubmit={submit}
              className={sideDrawerFormClassName()}
              aria-busy={detail.isFetching || save.isPending}
              noValidate
            >
              {((isUpdate && detail.isLoading) ||
                (!isUpdate && moneyContext.isLoading)) && <LoadingState />}
              {!isUpdate && moneyContext.isError && (
                <ErrorState
                  title={t('Failed to load')}
                  onRetry={() => {
                    void moneyContext.refetch()
                  }}
                />
              )}
              {isUpdate && detail.isError && (
                <ErrorState
                  title={t('Failed to load')}
                  description={getServerErrorMessage(
                    detail.error,
                    t('Failed to load')
                  )}
                  onRetry={() => {
                    void detail.refetch()
                  }}
                />
              )}
              {isUpdate && detail.data?.redeemed && (
                <p role='alert'>
                  {t('Used redemption codes cannot be changed')}
                </p>
              )}
              <fieldset
                disabled={!ready || save.isPending}
                className='contents'
              >
                <SideDrawerSection>
                  {textFields.map((item) => (
                    <FormField
                      key={item.name}
                      control={form.control}
                      name={item.name}
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{item.label}</FormLabel>
                          <FormControl>
                            <Input
                              {...field}
                              placeholder={item.placeholder}
                              inputMode={
                                item.name.includes('amount')
                                  ? 'decimal'
                                  : 'text'
                              }
                            />
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  ))}
                  {currency === 'CNY' && (
                    <Button
                      type='button'
                      variant='outline'
                      onClick={() =>
                        form.setValue(
                          'credit_amount',
                          form.getValues('paid_amount_cny'),
                          { shouldValidate: true, shouldDirty: true }
                        )
                      }
                    >
                      {t('Use payment amount as wallet credit')}
                    </Button>
                  )}
                  <FormField
                    control={form.control}
                    name='sales_evidence'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Sales evidence (optional)')}</FormLabel>
                        <FormControl>
                          <Textarea {...field} rows={3} />
                        </FormControl>
                        <FormDescription>
                          {t(
                            'Record the sale reference or receipt details. Do not paste redemption codes here.'
                          )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  <FormField
                    control={form.control}
                    name='expired_time'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Code validity')}</FormLabel>
                        <FormControl>
                          <DateTimePicker
                            timeZone='Asia/Shanghai'
                            value={field.value ?? undefined}
                            onChange={(date) => field.onChange(date ?? null)}
                            placeholder={t('Never expires')}
                          />
                        </FormControl>
                        <div className='flex flex-wrap gap-2'>
                          <Button
                            type='button'
                            variant='outline'
                            size='sm'
                            onClick={() =>
                              form.setValue('expired_time', null, {
                                shouldValidate: true,
                              })
                            }
                          >
                            {t('Never')}
                          </Button>
                          <Button
                            type='button'
                            variant='outline'
                            size='sm'
                            onClick={() =>
                              form.setValue(
                                'expired_time',
                                addTimeToDate(0, 7, 0) ?? null,
                                { shouldValidate: true }
                              )
                            }
                          >
                            {t('7 days')}
                          </Button>
                          <Button
                            type='button'
                            variant='outline'
                            size='sm'
                            onClick={() =>
                              form.setValue(
                                'expired_time',
                                addTimeToDate(1, 0, 0) ?? null,
                                { shouldValidate: true }
                              )
                            }
                          >
                            {t('1 month')}
                          </Button>
                        </div>
                        <FormDescription>
                          {t(
                            'The code must be redeemed before this time. Credited paid funds do not expire.'
                          )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                  {!isUpdate && (
                    <FormField
                      control={form.control}
                      name='count'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Quantity')}</FormLabel>
                          <FormControl>
                            <Input
                              {...field}
                              type='number'
                              min={1}
                              max={100}
                              step={1}
                              onChange={(event) =>
                                field.onChange(
                                  event.target.value === ''
                                    ? 0
                                    : Number(event.target.value)
                                )
                              }
                            />
                          </FormControl>
                          <FormDescription>
                            {t(
                              'Create multiple redemption codes at once (1-100)'
                            )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  )}
                  {form.formState.errors.root?.server?.message && (
                    <div className='space-y-2'>
                      <p role='alert' className='text-destructive text-sm'>
                        {form.formState.errors.root.server.message}
                      </p>
                      <Button
                        type='button'
                        variant='outline'
                        onClick={() => {
                          form.clearErrors('root')
                          setSnapshot(null)
                          void (isUpdate
                            ? detail.refetch()
                            : moneyContext.refetch())
                        }}
                      >
                        {t('Refresh')}
                      </Button>
                    </div>
                  )}
                </SideDrawerSection>
              </fieldset>
            </form>
          </Form>
          <SheetFooter className={sideDrawerFooterClassName()}>
            <SheetClose
              render={<Button variant='outline' disabled={save.isPending} />}
            >
              {t('Close')}
            </SheetClose>
            <Button
              form='redemption-form'
              type='submit'
              disabled={save.isPending || !ready}
            >
              {save.isPending ? t('Saving...') : t('Save changes')}
            </Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>
      {createdCodes && (
        <RedemptionsExportDialog
          data={createdCodes}
          onClose={() => setCreatedCodes(null)}
        />
      )}
    </>
  )
}
