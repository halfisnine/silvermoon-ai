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
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { DateTimePicker } from '@/components/datetime-picker'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { RewardSummary } from '@/features/marketing-admin/activities/summary'
import {
  formatMarketingDate,
  formatMarketingMoney,
} from '@/features/marketing-admin/format'
/* eslint-disable react/no-array-index-key -- Immutable ordered reward snapshots have no editable row state. */
import { useAuthStore } from '@/stores/auth-store'

import { userMarketingGet } from '../api'
import type { Gift, MarketingPage, RewardRecord } from '../types'

export function ActivityRewardsPage(props: {
  tab?: string
  source?: string
  friend?: string
}) {
  const { t } = useTranslation()
  const [tab, setTab] = useState(props.tab === 'gifts' ? 'gifts' : 'records')
  return (
    <SectionPageLayout fixedContent>
      <SectionPageLayout.Title>{t('Activity rewards')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='flex h-full min-h-0 flex-col gap-3'>
          <Tabs value={tab} onValueChange={(value) => setTab(String(value))}>
            <TabsList>
              <TabsTrigger value='records'>{t('Reward records')}</TabsTrigger>
              <TabsTrigger value='gifts'>{t('Gift details')}</TabsTrigger>
            </TabsList>
          </Tabs>
          {tab === 'gifts' ? (
            <GiftList />
          ) : (
            <RewardList source={props.source} friend={props.friend} />
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
function rewardResultLabel(state: string, t: (key: string) => string): string {
  switch (state) {
    case 'credited':
    case 'issued':
    case 'awarded':
    case 'complete':
    case 'completed':
      return t('Granted')
    case 'pending':
      return t('Pending delivery')
    case 'shortage':
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
function RewardList(props: { source?: string; friend?: string }) {
  const userId = useAuthStore((state) => state.auth.user?.id)

  const { t, i18n } = useTranslation()
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 20 })
  const [source, setSource] = useState(props.source || '')
  const [friend, setFriend] = useState(
    props.source === 'invitation' ? props.friend || '' : ''
  )
  const [from, setFrom] = useState<Date>()
  const [until, setUntil] = useState<Date>()
  const [selected, setSelected] = useState<RewardRecord>()
  const valid = !from || !until || until >= from
  const params = {
    kind: source === 'lottery' ? 'lottery' : 'activity',
    activity_kind: source === 'lottery' ? '' : source,
    ...(friend ? { friend_username: friend } : {}),
    page: pagination.pageIndex + 1,
    page_size: pagination.pageSize,
    starts_at: from ? Math.floor(from.getTime() / 1000) : 0,
    // Date-only filters include the selected end day. Both pickers emit midnight
    // in the site's fixed +08:00 civil timezone, which has no daylight changes.
    ends_at: until ? Math.floor(until.getTime() / 1000) + 86400 : 0,
  }
  const query = useQuery({
    queryKey: ['marketing-user', userId, 'rewards', params],
    queryFn: () =>
      userMarketingGet<MarketingPage<RewardRecord>>('/rewards', params),
    enabled: !!userId && valid,
    refetchInterval: 30000,
  })
  const columns: ColumnDef<RewardRecord>[] = [
    {
      id: 'time',
      header: t('Time'),
      cell: ({ row }) =>
        formatMarketingDate(row.original.occurred_at, i18n.language),
    },
    { accessorKey: 'source_name', header: t('Source') },
    {
      id: 'rewards',
      header: t('Rewards'),
      cell: ({ row }) =>
        row.original.source.no_prize ? (
          t('No prize')
        ) : (
          <RewardSummary rewards={row.original.rewards} />
        ),
    },
    {
      id: 'state',
      header: t('Result'),
      cell: ({ row }) =>
        row.original.source.no_prize
          ? t('No prize')
          : rewardResultLabel(row.original.state, t),
    },
    {
      id: 'actions',
      header: t('Details'),
      cell: ({ row }) => (
        <Button
          size='sm'
          variant='outline'
          onClick={() => setSelected(row.original)}
        >
          {t('Details')}
        </Button>
      ),
    },
  ]
  const { table } = useDataTable({
    data: valid ? (query.data?.items ?? []) : [],
    columns,
    pagination,
    onPaginationChange: setPagination,
    manualPagination: true,
    enableRowSelection: false,
    totalCount: valid ? (query.data?.total ?? 0) : 0,
  })
  const reset = () => setPagination({ ...pagination, pageIndex: 0 })
  return (
    <>
      {friend && (
        <div className='flex flex-wrap items-center gap-2 text-sm'>
          <span>
            {t('Invited friend')}: <strong>{friend}</strong>
          </span>
          <Button
            size='sm'
            variant='outline'
            onClick={() => {
              setFriend('')
              reset()
            }}
          >
            {t('Clear')}
          </Button>
        </div>
      )}
      {query.isError ? (
        <ErrorState onRetry={() => void query.refetch()} />
      ) : (
        <DataTablePage
          table={table}
          columns={columns}
          isLoading={valid && query.isPending}
          isFetching={query.isFetching}
          emptyTitle={t('No reward records found')}
          toolbar={
            <div className='grid gap-3 sm:grid-cols-3'>
              <div className='flex flex-col gap-2'>
                <Label htmlFor='reward-source'>{t('Reward source')}</Label>
                <NativeSelect
                  id='reward-source'
                  value={source}
                  onChange={(e) => {
                    setSource(e.target.value)
                    setFriend('')
                    reset()
                  }}
                >
                  <NativeSelectOption value=''>
                    {t('Activity-issued rewards')}
                  </NativeSelectOption>
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
                    {t('Lottery results')}
                  </NativeSelectOption>
                </NativeSelect>
              </div>
              <div className='flex flex-col gap-2'>
                <Label htmlFor='reward-from'>{t('From')}</Label>
                <DateTimePicker
                  id='reward-from'
                  timeZone='Asia/Shanghai'
                  hideTime
                  value={from}
                  onChange={(date) => {
                    setFrom(date)
                    reset()
                  }}
                />
              </div>
              <div className='flex flex-col gap-2'>
                <Label htmlFor='reward-until'>{t('Until')}</Label>
                <DateTimePicker
                  id='reward-until'
                  timeZone='Asia/Shanghai'
                  hideTime
                  value={until}
                  onChange={(date) => {
                    setUntil(date)
                    reset()
                  }}
                  aria-invalid={!valid}
                />
                {!valid && (
                  <p role='alert'>
                    {t('End date must be on or after start date')}
                  </p>
                )}
              </div>
            </div>
          }
        />
      )}
      {selected && (
        <RewardDetails
          kind={selected.kind}
          id={selected.id}
          onClose={() => setSelected(undefined)}
        />
      )}
    </>
  )
}
function RewardDetails(props: {
  kind: string
  id: number
  onClose: () => void
}) {
  const userId = useAuthStore((state) => state.auth.user?.id)

  const { t, i18n } = useTranslation()
  const query = useQuery({
    queryKey: ['marketing-user', userId, 'reward', props.kind, props.id],
    queryFn: () =>
      userMarketingGet<RewardRecord>(`/rewards/${props.kind}/${props.id}`),
    refetchInterval: 30000,
  })
  const r = query.data
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
      {r && (
        <div className='flex flex-col gap-4 text-sm'>
          <h3 className='font-semibold break-words'>{r.source_name}</h3>
          <p>
            {formatMarketingDate(r.occurred_at, i18n.language)} ·{' '}
            {r.source.no_prize ? t('No prize') : rewardResultLabel(r.state, t)}
          </p>
          {r.source.reason_key && <p>{t(String(r.source.reason_key))}</p>}
          {r.state === 'pending' && (
            <p>
              {t(
                'Some rewards may already be credited. Pending items are delivered automatically; do not claim them again.'
              )}
            </p>
          )}
          {r.source.checkin_date && (
            <p>
              {t('Check-in date')}:{' '}
              {formatMarketingDate(
                Date.parse(`${r.source.checkin_date}T00:00:00+08:00`) / 1000,
                i18n.language
              )}
            </p>
          )}
          {r.source.friend_username && (
            <p>
              {t('Invited friend')}: {String(r.source.friend_username)}
            </p>
          )}
          {r.source.opportunity_activity && (
            <p>
              {t('Opportunity source activity')}:{' '}
              {String(r.source.opportunity_activity)}
            </p>
          )}
          {r.source.prize_name && (
            <p>
              {t('Prize')}: {String(r.source.prize_name)}
            </p>
          )}
          {r.source.paid_cny && (
            <p>
              {t('Actual payment (CNY)')}:{' '}
              {formatMarketingMoney(
                String(r.source.paid_cny),
                'CNY',
                i18n.language
              )}
            </p>
          )}
          {!r.source.no_prize &&
            r.rewards.map((reward, index) => (
              <div key={`${reward.kind}-${index}`} className='border-t pt-3'>
                <RewardSummary rewards={[reward]} />
                {reward.amount &&
                  reward.calculation &&
                  reward.calculation !== 'fixed' && (
                    <p>
                      {t('Calculated gift')}:{' '}
                      {formatMarketingMoney(
                        reward.amount,
                        reward.currency || 'CNY',
                        i18n.language
                      )}
                    </p>
                  )}
                {reward.state && <p>{rewardResultLabel(reward.state, t)}</p>}
                {!!reward.credited_at && (
                  <p>
                    {t('Credited at')}:{' '}
                    {formatMarketingDate(reward.credited_at, i18n.language)}
                  </p>
                )}
                {reward.expires_at !== undefined && !!reward.credited_at && (
                  <p>
                    {t('Expires at')}:{' '}
                    {reward.expires_at
                      ? formatMarketingDate(reward.expires_at, i18n.language)
                      : t('Never expires')}
                  </p>
                )}
              </div>
            ))}
        </div>
      )}
    </Dialog>
  )
}
function GiftList() {
  const userId = useAuthStore((state) => state.auth.user?.id)

  const { t, i18n } = useTranslation()
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 20 })
  const [selected, setSelected] = useState<Gift>()
  const query = useQuery({
    queryKey: ['marketing-user', userId, 'gifts', pagination],
    queryFn: () =>
      userMarketingGet<MarketingPage<Gift>>('/gifts', {
        page: pagination.pageIndex + 1,
        page_size: pagination.pageSize,
      }),
    refetchInterval: 30000,
  })
  const columns: ColumnDef<Gift>[] = [
    {
      id: 'source_name',
      header: t('Source'),
      cell: ({ row }) =>
        row.original.source_name_key
          ? t(row.original.source_name_key)
          : row.original.source_name,
    },
    ...(['granted_amount', 'consumed_amount', 'remaining_amount'] as const).map(
      (key, index) => ({
        id: key,
        header: [t('Gift received'), t('Consumed'), t('Remaining')][index],
        cell: ({ row }: { row: { original: Gift } }) =>
          formatMarketingMoney(
            row.original[key],
            row.original.currency,
            i18n.language
          ),
      })
    ),
    {
      id: 'expiry',
      header: t('Validity'),
      cell: ({ row }) =>
        row.original.expires_at
          ? formatMarketingDate(row.original.expires_at, i18n.language)
          : t('Permanent gift (no expiry)'),
    },
    {
      id: 'state',
      header: t('Status'),
      cell: ({ row }) => giftState(row.original.state, t),
    },
    {
      id: 'detail',
      header: t('Details'),
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
    data: query.data?.items ?? [],
    columns,
    pagination,
    onPaginationChange: setPagination,
    manualPagination: true,
    enableRowSelection: false,
    totalCount: query.data?.total ?? 0,
  })
  return (
    <>
      {query.isError ? (
        <ErrorState onRetry={() => void query.refetch()} />
      ) : (
        <DataTablePage
          table={table}
          columns={columns}
          isLoading={query.isPending}
          isFetching={query.isFetching}
          emptyTitle={t('No gifts yet')}
        />
      )}
      {selected && (
        <GiftDetails id={selected.id} onClose={() => setSelected(undefined)} />
      )}
    </>
  )
}
function giftState(state: string, t: (key: string) => string) {
  if (state === 'expired') return t('Expired')
  if (state === 'used') return t('Fully used')
  if (state === 'cleared') return t('Cleared after refund')
  return t('Available')
}
function GiftDetails(props: { id: number; onClose: () => void }) {
  const userId = useAuthStore((state) => state.auth.user?.id)

  const { t, i18n } = useTranslation()
  const query = useQuery({
    queryKey: ['marketing-user', userId, 'gift', props.id],
    queryFn: () => userMarketingGet<Gift>(`/gifts/${props.id}`),
    refetchInterval: 30000,
  })
  const gift = query.data
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Gift details')}
    >
      {query.isPending && <LoadingState />}
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      {gift && (
        <dl className='grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-3 text-sm'>
          <dt>{t('Source')}</dt>
          <dd className='break-words'>
            {gift.source_name_key ? t(gift.source_name_key) : gift.source_name}
          </dd>
          <dt>{t('Status')}</dt>
          <dd>{giftState(gift.state, t)}</dd>
          {[
            [t('Gift received'), gift.granted_amount],
            [t('Consumed'), gift.consumed_amount],
            [t('Remaining'), gift.remaining_amount],
            [t('Expired unused gift'), gift.expired_amount],
            [t('Cleared after refund'), gift.cleared_amount],
          ].map(([label, value]) => (
            <div key={label} className='contents'>
              <dt>{label}</dt>
              <dd>
                {formatMarketingMoney(value, gift.currency, i18n.language)}
              </dd>
            </div>
          ))}
          <dt>{t('Credited at')}</dt>
          <dd>{formatMarketingDate(gift.credited_at, i18n.language)}</dd>
          <dt>{t('Expires at')}</dt>
          <dd>
            {gift.expires_at
              ? formatMarketingDate(gift.expires_at, i18n.language)
              : t('Never expires')}
          </dd>
          <dd className='text-muted-foreground col-span-2'>
            {t('Expired or cleared gifts are not counted as consumption.')}
          </dd>
          {gift.state === 'expired' && Number(gift.remaining_amount) > 0 && (
            <dd className='text-muted-foreground col-span-2'>
              {t(
                'A request started before expiry is still settling. Its final gift consumption will appear here; expired gifts cannot fund new requests.'
              )}
            </dd>
          )}
        </dl>
      )}
    </Dialog>
  )
}
