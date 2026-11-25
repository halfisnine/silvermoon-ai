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
import { useQuery } from '@tanstack/react-query'
import type { ColumnDef } from '@tanstack/react-table'
import { useState } from 'react'
/* eslint-disable react/no-array-index-key -- Reward arrays are immutable ordered snapshots with no row identity or editable child state. */
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { DateTimePicker } from '@/components/datetime-picker'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { toIntlLocale } from '@/i18n/languages'

import { RewardSummary } from '../activities/summary'
import { getAllActivityOptions, marketingGet } from '../api'
import { formatMarketingDate, formatMarketingMoney } from '../format'
import type { MarketingPage, MarketingReward } from '../types'

type RewardRecord = {
  id: number
  kind: string
  username: string
  source_name: string
  state: string
  occurred_at: number
  credited_at: number
  rewards: MarketingReward[]
  source: Record<string, string | number | boolean>
}
export function MarketingRewardsPage() {
  const { t, i18n } = useTranslation()
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 20 })
  const [kind, setKind] = useState('activity')
  const [username, setUsername] = useState('')
  const [activityID, setActivityID] = useState(0)
  const [startsAt, setStartsAt] = useState<Date>()
  const [endsAt, setEndsAt] = useState<Date>()
  const [selected, setSelected] = useState<RewardRecord>()
  const validRange = !startsAt || !endsAt || endsAt > startsAt
  const params = {
    page: pagination.pageIndex + 1,
    page_size: pagination.pageSize,
    kind,
    username,
    activity_id: activityID,
    starts_at: startsAt ? Math.floor(startsAt.getTime() / 1000) : 0,
    ends_at: endsAt ? Math.floor(endsAt.getTime() / 1000) : 0,
  }
  const query = useQuery({
    queryKey: ['marketing-admin', 'rewards', params],
    queryFn: () =>
      marketingGet<MarketingPage<RewardRecord>>('/rewards', params),
    enabled: validRange,
  })
  const resetPage = () => setPagination({ ...pagination, pageIndex: 0 })
  const columns: ColumnDef<RewardRecord>[] = [
    {
      id: 'time',
      header: t('Time'),
      cell: ({ row }) =>
        formatMarketingDate(row.original.occurred_at, i18n.language),
    },
    { accessorKey: 'username', header: t('User') },
    { accessorKey: 'source_name', header: t('Source') },
    {
      id: 'trigger',
      header: t('Reward trigger'),
      cell: ({ row }) => rewardTrigger(row.original, t),
    },
    {
      id: 'rewards',
      header: t('Rewards'),
      cell: ({ row }) => <RecordRewards record={row.original} />,
    },
    {
      id: 'state',
      header: t('Result'),
      cell: ({ row }) =>
        row.original.source.no_prize
          ? t('No prize')
          : rewardStateLabel(row.original.state, t),
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) => (
        <Button
          variant='outline'
          size='sm'
          onClick={() => setSelected(row.original)}
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
  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>
          {t('Marketing reward records')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Content>
          {query.isError ? (
            <ErrorState onRetry={() => void query.refetch()} />
          ) : (
            <DataTablePage
              table={table}
              columns={columns}
              isLoading={validRange && query.isPending}
              isFetching={query.isFetching}
              emptyTitle={t('No reward records found')}
              toolbar={
                <div className='flex flex-col gap-3'>
                  <FieldGroup className='grid items-start gap-3 sm:grid-cols-2 xl:grid-cols-4'>
                    <Field className='min-w-0'>
                      <FieldLabel htmlFor='rewards-source'>
                        {t('Reward source')}
                      </FieldLabel>
                      <NativeSelect
                        id='rewards-source'
                        aria-label={t('Reward source')}
                        value={kind}
                        onChange={(e) => {
                          setKind(e.target.value)
                          setActivityID(0)
                          resetPage()
                        }}
                      >
                        <NativeSelectOption value='activity'>
                          {t('Activity rewards')}
                        </NativeSelectOption>
                        <NativeSelectOption value='lottery'>
                          {t('Lottery results')}
                        </NativeSelectOption>
                      </NativeSelect>
                    </Field>
                    <Field className='min-w-0'>
                      <FieldLabel htmlFor='rewards-username'>
                        {t('Username')}
                      </FieldLabel>
                      <Input
                        id='rewards-username'
                        aria-label={t('Username')}
                        placeholder={t('Username')}
                        value={username}
                        onChange={(e) => {
                          setUsername(e.target.value)
                          resetPage()
                        }}
                      />
                    </Field>
                    {kind === 'activity' && (
                      <ActivityFilter
                        value={activityID}
                        onChange={(id) => {
                          setActivityID(id)
                          resetPage()
                        }}
                      />
                    )}
                  </FieldGroup>
                  <FieldGroup className='grid items-start gap-3 sm:grid-cols-2'>
                    <Field className='min-w-0'>
                      <FieldLabel htmlFor='rewards-start'>
                        {t('From')}
                      </FieldLabel>
                      <DateTimePicker
                        id='rewards-start'
                        timeZone='Asia/Shanghai'
                        className='min-w-0 flex-wrap [&>button:first-child]:min-w-40'
                        value={startsAt}
                        onChange={(d) => {
                          setStartsAt(d)
                          resetPage()
                        }}
                      />
                    </Field>
                    <Field className='min-w-0'>
                      <FieldLabel htmlFor='rewards-end'>
                        {t('Until (exclusive)')}
                      </FieldLabel>
                      <DateTimePicker
                        id='rewards-end'
                        timeZone='Asia/Shanghai'
                        className='min-w-0 flex-wrap [&>button:first-child]:min-w-40'
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
                  </FieldGroup>
                </div>
              }
            />
          )}
        </SectionPageLayout.Content>
      </SectionPageLayout>
      {selected && (
        <RewardDetail
          record={selected}
          onClose={() => setSelected(undefined)}
        />
      )}
    </>
  )
}
function ActivityFilter(props: {
  value: number
  onChange: (value: number) => void
}) {
  const { t } = useTranslation()
  const [keyword, setKeyword] = useState('')
  const query = useQuery({
    queryKey: ['marketing-admin', 'activity-options', 'all', keyword],
    queryFn: () => getAllActivityOptions(keyword),
  })
  return (
    <FieldGroup className='grid min-w-0 items-start gap-3 sm:col-span-2 sm:grid-cols-2'>
      <Field className='min-w-0'>
        <FieldLabel htmlFor='rewards-activity-search'>
          {t('Search activities')}
        </FieldLabel>
        <Input
          id='rewards-activity-search'
          aria-label={t('Search activities')}
          placeholder={t('Search activities')}
          value={keyword}
          onChange={(e) => {
            setKeyword(e.target.value)
            props.onChange(0)
          }}
        />
      </Field>
      <Field className='min-w-0'>
        <FieldLabel htmlFor='rewards-activity'>{t('Activity')}</FieldLabel>
        {query.isError ? (
          <ErrorState onRetry={() => void query.refetch()} />
        ) : (
          <NativeSelect
            id='rewards-activity'
            aria-label={t('Activity')}
            disabled={query.isPending}
            value={props.value}
            onChange={(e) => props.onChange(Number(e.target.value))}
          >
            <NativeSelectOption value={0}>
              {t('All activities')}
            </NativeSelectOption>
            {query.data?.map((a) => (
              <NativeSelectOption key={a.id} value={a.id}>
                {a.activity.name}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        )}
      </Field>
    </FieldGroup>
  )
}
function RecordRewards(props: { record: RewardRecord }) {
  const { t, i18n } = useTranslation()
  if (props.record.source.no_prize) return <span>{t('No prize')}</span>
  return (
    <div className='flex flex-col gap-1'>
      <RewardSummary rewards={props.record.rewards} />
      {props.record.rewards
        .filter((r) => r.calculation !== 'fixed' && r.amount)
        .map((r, i) => (
          <p key={i}>
            {t('Calculated gift')}:{' '}
            {formatMarketingMoney(
              r.amount || '0',
              r.currency || 'CNY',
              i18n.language
            )}
          </p>
        ))}
    </div>
  )
}
export function RewardDetail(props: {
  record: Pick<RewardRecord, 'kind' | 'id'>
  onClose: () => void
}) {
  const { t, i18n } = useTranslation()
  const query = useQuery({
    queryKey: [
      'marketing-admin',
      'reward-detail',
      props.record.kind,
      props.record.id,
    ],
    queryFn: () =>
      marketingGet<RewardRecord>(
        `/rewards/${props.record.kind}/${props.record.id}`
      ),
  })
  const record = query.data
  const source = record?.source
  const fields: [string, string][] = []
  if (source) {
    const labels: Record<string, string> = {
      redemption_name: t('Redemption code name'),
      sales_channel: t('Sales channel'),
      sales_batch: t('Sales batch'),
      sales_evidence: t('Sales evidence'),
      payment_reference: t('Payment reference'),
      friend_username: t('Invited friend'),
      prize_name: t('Prize'),
      opportunity_activity: t('Opportunity source activity'),
    }
    for (const [key, label] of Object.entries(labels)) {
      if (source[key]) fields.push([label, String(source[key])])
    }
    fields.push([t('Reward trigger'), rewardTrigger(record as RewardRecord, t)])
    if (source.reason_key) {
      fields.push([t('Reason'), t(String(source.reason_key))])
    }
    if (source.paid_cny) {
      fields.push([
        t('Actual payment (CNY)'),
        formatMarketingMoney(String(source.paid_cny), 'CNY', i18n.language),
      ])
    }
    if (source.posted_at) {
      fields.push([
        t('Payment posted at'),
        formatMarketingDate(Number(source.posted_at), i18n.language),
      ])
    }
    if (source.expires_at !== undefined) {
      fields.push([
        t('Expires at'),
        Number(source.expires_at)
          ? formatMarketingDate(Number(source.expires_at), i18n.language)
          : t('Never expires'),
      ])
    }
    if (source.checkin_date) {
      fields.push([
        t('Check-in date'),
        new Intl.DateTimeFormat(toIntlLocale(i18n.language), {
          year: 'numeric',
          month: 'long',
          day: 'numeric',
          timeZone: 'Asia/Shanghai',
        }).format(new Date(`${source.checkin_date}T00:00:00+08:00`)),
      ])
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Reward source details')}
    >
      {query.isPending && <LoadingState />}
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      {record && (
        <div className='flex flex-col gap-4'>
          <dl className='grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm'>
            <dt>{t('User')}</dt>
            <dd>{record.username}</dd>
            <dt>{t('Source')}</dt>
            <dd>{record.source_name}</dd>
            <dt>{t('Result')}</dt>
            <dd>
              {record.source.no_prize
                ? t('No prize')
                : rewardStateLabel(record.state, t)}
            </dd>
            <dt>{t('Occurred at')}</dt>
            <dd>{formatMarketingDate(record.occurred_at, i18n.language)}</dd>
            {record.credited_at > 0 && (
              <>
                <dt>{t('Credited at')}</dt>
                <dd>
                  {formatMarketingDate(record.credited_at, i18n.language)}
                </dd>
              </>
            )}
            {fields.map(([label, value]) => (
              <div key={label} className='contents'>
                <dt>{label}</dt>
                <dd className='break-words'>{value}</dd>
              </div>
            ))}
          </dl>
          <RecordRewards record={record} />
          <ul className='flex flex-col gap-2 text-sm'>
            {record.rewards.map((r, i) =>
              r.credited_at ? (
                <li key={i}>
                  {r.amount &&
                    formatMarketingMoney(
                      r.amount,
                      r.currency || 'CNY',
                      i18n.language
                    )}{' '}
                  · {t('Credited at')}:{' '}
                  {formatMarketingDate(r.credited_at, i18n.language)}
                  {r.expires_at !== undefined && (
                    <>
                      {' '}
                      · {t('Expires at')}:{' '}
                      {r.expires_at
                        ? formatMarketingDate(r.expires_at, i18n.language)
                        : t('Never expires')}
                    </>
                  )}
                </li>
              ) : null
            )}
          </ul>
        </div>
      )}
    </Dialog>
  )
}
function rewardStateLabel(state: string, t: (key: string) => string): string {
  switch (state) {
    case 'credited':
    case 'issued':
    case 'awarded':
    case 'complete':
      return t('Granted')
    case 'pending':
      return t('Pending delivery')
    case 'shortage':
    case 'no_capacity':
    case 'capacity_exhausted':
    case 'insufficient_capacity':
      return t('Opportunity limit reached')
    case 'probability_miss':
      return t('No opportunities awarded by probability')
    case 'pool_expired':
      return t('Prize pool expired')
    case 'zero_amount':
      return t('Amount below minimum precision')
    case 'cleared':
      return t('Cleared after refund')
    case 'no_prize':
      return t('No prize')
    default:
      return t('Delivery needs attention')
  }
}

function rewardTrigger(
  record: RewardRecord,
  t: (key: string) => string
): string {
  if (record.kind === 'lottery') return t('Lottery results')
  if (record.source.friend_username) return t('Invitation rewards')
  switch (record.source.trigger) {
    case 'registration':
      return t('Registration succeeds')
    case 'payment':
      return t('Recharge rewards')
    case 'checkin':
      return t('Check-in rewards')
    default:
      return t('Activity rewards')
  }
}
