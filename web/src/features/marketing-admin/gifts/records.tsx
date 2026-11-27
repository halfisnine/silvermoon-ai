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
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { DateTimePicker } from '@/components/datetime-picker'
import { ErrorState } from '@/components/error-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { useDebounce } from '@/hooks/use-debounce'

import { marketingGet } from '../api'
import {
  formatMarketingDate,
  formatMarketingDuration,
  formatMarketingMoney,
} from '../format'
import type { MarketingPage } from '../types'
import { giftState } from './labels'
import type { AdminGift, GiftBatch } from './types'

export function GiftRecords(props: {
  batches: boolean
  onBatch: (id: number) => void
  onGift: (id: number) => void
}) {
  const { t, i18n } = useTranslation()
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 20 })
  const [search, setSearch] = useState('')
  const keyword = useDebounce(search, 300)
  const [kind, setKind] = useState('')
  const [state, setState] = useState('')
  const [validity, setValidity] = useState('')
  const [startsAt, setStartsAt] = useState<Date>()
  const [endsAt, setEndsAt] = useState<Date>()
  const validRange = !startsAt || !endsAt || endsAt > startsAt
  const params = {
    page: pagination.pageIndex + 1,
    page_size: pagination.pageSize,
    keyword,
    kind,
    state,
    validity,
    starts_at: startsAt ? Math.floor(startsAt.getTime() / 1000) : 0,
    ends_at: endsAt ? Math.floor(endsAt.getTime() / 1000) : 0,
  }
  const resource = props.batches ? 'gift-batches' : 'gifts'
  const query = useQuery({
    queryKey: ['marketing-admin', resource, params],
    queryFn: () =>
      marketingGet<MarketingPage<AdminGift | GiftBatch>>(
        `/${resource}`,
        params
      ),
    enabled: validRange,
    placeholderData: keepPreviousData,
    refetchInterval: 30000,
  })
  const resetPage = () => setPagination((p) => ({ ...p, pageIndex: 0 }))
  const columns: ColumnDef<AdminGift | GiftBatch>[] = props.batches
    ? [
        {
          id: 'batch',
          header: t('Batch'),
          cell: ({ row }) => `#${row.original.id}`,
        },
        {
          id: 'time',
          header: t('Time'),
          cell: ({ row }) =>
            formatMarketingDate(
              (row.original as GiftBatch).created_at,
              i18n.language
            ),
        },
        {
          id: 'operator',
          header: t('Operator'),
          cell: ({ row }) => (row.original as GiftBatch).actor_name,
        },
        {
          id: 'amount',
          header: t('Gift amount per user'),
          cell: ({ row }) => {
            const b = row.original as GiftBatch
            return formatMarketingMoney(b.amount, b.currency, i18n.language)
          },
        },
        {
          id: 'total',
          header: t('Credited total'),
          cell: ({ row }) => {
            const b = row.original as GiftBatch
            return formatMarketingMoney(
              b.total_amount,
              b.currency,
              i18n.language
            )
          },
        },
        {
          id: 'validity',
          header: t('Validity'),
          cell: ({ row }) => {
            const b = row.original as GiftBatch
            return b.valid_seconds
              ? formatMarketingDuration(b.valid_seconds, i18n.language)
              : t('Never expires')
          },
        },
        {
          id: 'result',
          header: t('Result'),
          cell: ({ row }) => {
            const b = row.original as GiftBatch
            return (
              <span className='text-xs'>
                {t(
                  'Issuance results: {{credited}} credited, {{failed}} failed, {{pending}} pending',
                  { credited: b.credited, failed: b.failed, pending: b.pending }
                )}
              </span>
            )
          },
        },
        {
          id: 'reason',
          header: t('Internal issuance reason'),
          cell: ({ row }) => (
            <span
              className='line-clamp-2 max-w-56 break-words'
              title={(row.original as GiftBatch).reason}
            >
              {(row.original as GiftBatch).reason}
            </span>
          ),
        },
        {
          id: 'actions',
          header: t('Actions'),
          cell: ({ row }) => (
            <Button
              variant='outline'
              size='sm'
              onClick={() => props.onBatch(row.original.id)}
            >
              {t('Details')}
            </Button>
          ),
        },
      ]
    : [
        {
          id: 'user',
          header: t('User'),
          cell: ({ row }) => {
            const g = row.original as AdminGift
            return (
              <div>
                <p>{g.username || '—'}</p>
                <p className='text-muted-foreground text-xs'>ID {g.user_id}</p>
              </div>
            )
          },
        },
        {
          id: 'source',
          header: t('Source'),
          cell: ({ row }) => {
            const g = row.original as AdminGift
            return g.source_name_key ? t(g.source_name_key) : g.source_name
          },
        },
        ...(
          ['granted_amount', 'consumed_amount', 'remaining_amount'] as const
        ).map((key, index) => ({
          id: key,
          header: [t('Gift received'), t('Consumed'), t('Remaining')][index],
          cell: ({ row }: { row: { original: AdminGift | GiftBatch } }) => {
            const g = row.original as AdminGift
            return formatMarketingMoney(g[key], g.currency, i18n.language)
          },
        })),
        {
          id: 'credited',
          header: t('Credited at'),
          cell: ({ row }) =>
            formatMarketingDate(
              (row.original as AdminGift).credited_at,
              i18n.language
            ),
        },
        {
          id: 'expiry',
          header: t('Expires at'),
          cell: ({ row }) => {
            const g = row.original as AdminGift
            return g.expires_at
              ? formatMarketingDate(g.expires_at, i18n.language)
              : t('Never expires')
          },
        },
        {
          id: 'state',
          header: t('Status'),
          cell: ({ row }) => (
            <Badge variant='outline'>
              {giftState((row.original as AdminGift).state, t)}
            </Badge>
          ),
        },
        {
          id: 'actions',
          header: t('Actions'),
          cell: ({ row }) => (
            <Button
              variant='outline'
              size='sm'
              onClick={() => props.onGift(row.original.id)}
            >
              {t('Details')}
            </Button>
          ),
        },
      ]
  const { table } = useDataTable({
    data: validRange ? (query.data?.items ?? []) : [],
    columns,
    pagination,
    onPaginationChange: setPagination,
    manualPagination: true,
    enableRowSelection: false,
    totalCount: validRange ? (query.data?.total ?? 0) : 0,
  })
  return query.isError ? (
    <ErrorState onRetry={() => void query.refetch()} />
  ) : (
    <DataTablePage
      table={table}
      columns={columns}
      isLoading={validRange && query.isPending}
      isFetching={query.isFetching}
      emptyTitle={
        props.batches
          ? t('No issuance records found')
          : t('No gift credits found')
      }
      toolbar={
        <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-4'>
          <Field>
            <FieldLabel htmlFor='gift-record-search'>{t('User')}</FieldLabel>
            <Input
              id='gift-record-search'
              placeholder={t('Search by username, display name or user ID')}
              value={search}
              maxLength={160}
              onChange={(e) => {
                setSearch(e.target.value)
                resetPage()
              }}
            />
          </Field>
          {!props.batches && (
            <>
              <Field>
                <FieldLabel htmlFor='gift-source'>{t('Source')}</FieldLabel>
                <NativeSelect
                  id='gift-source'
                  value={kind}
                  onChange={(e) => {
                    setKind(e.target.value)
                    resetPage()
                  }}
                >
                  <NativeSelectOption value=''>{t('All')}</NativeSelectOption>
                  <NativeSelectOption value='registration'>
                    {t('Registration rewards')}
                  </NativeSelectOption>
                  <NativeSelectOption value='checkin'>
                    {t('Check-in rewards')}
                  </NativeSelectOption>
                  <NativeSelectOption value='recharge'>
                    {t('Recharge rewards')}
                  </NativeSelectOption>
                  <NativeSelectOption value='invitation'>
                    {t('Invitation rewards')}
                  </NativeSelectOption>
                  <NativeSelectOption value='lottery'>
                    {t('Lottery rewards')}
                  </NativeSelectOption>
                  <NativeSelectOption value='admin_gift'>
                    {t('Administrator gift')}
                  </NativeSelectOption>
                </NativeSelect>
              </Field>
              <Field>
                <FieldLabel htmlFor='gift-state'>{t('Status')}</FieldLabel>
                <NativeSelect
                  id='gift-state'
                  value={state}
                  onChange={(e) => {
                    setState(e.target.value)
                    resetPage()
                  }}
                >
                  <NativeSelectOption value=''>{t('All')}</NativeSelectOption>
                  <NativeSelectOption value='active'>
                    {t('Available')}
                  </NativeSelectOption>
                  <NativeSelectOption value='used'>
                    {t('Used up')}
                  </NativeSelectOption>
                  <NativeSelectOption value='expired'>
                    {t('Expired')}
                  </NativeSelectOption>
                  <NativeSelectOption value='cleared'>
                    {t('Cleared after refund')}
                  </NativeSelectOption>
                </NativeSelect>
              </Field>
              <Field>
                <FieldLabel htmlFor='gift-validity'>{t('Validity')}</FieldLabel>
                <NativeSelect
                  id='gift-validity'
                  value={validity}
                  onChange={(e) => {
                    setValidity(e.target.value)
                    resetPage()
                  }}
                >
                  <NativeSelectOption value=''>{t('All')}</NativeSelectOption>
                  <NativeSelectOption value='permanent'>
                    {t('Never expires')}
                  </NativeSelectOption>
                  <NativeSelectOption value='limited'>
                    {t('Timed gift')}
                  </NativeSelectOption>
                </NativeSelect>
              </Field>
            </>
          )}
          <Field>
            <FieldLabel htmlFor='gift-from'>{t('From')}</FieldLabel>
            <DateTimePicker
              compact
              id='gift-from'
              timeZone='Asia/Shanghai'
              value={startsAt}
              onChange={(d) => {
                setStartsAt(d)
                resetPage()
              }}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor='gift-until'>
              {t('Until (exclusive)')}
            </FieldLabel>
            <DateTimePicker
              compact
              id='gift-until'
              timeZone='Asia/Shanghai'
              value={endsAt}
              onChange={(d) => {
                setEndsAt(d)
                resetPage()
              }}
              aria-invalid={!validRange}
            />
            {!validRange && (
              <p role='alert' className='text-destructive text-sm'>
                {t('End date must be after start date')}
              </p>
            )}
          </Field>
        </div>
      }
    />
  )
}
