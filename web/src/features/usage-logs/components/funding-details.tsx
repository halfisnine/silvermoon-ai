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
import { lazy, Suspense, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  formatMarketingDate,
  formatMarketingMoney,
} from '@/features/marketing-admin/format'
import type {
  AccountingMoney,
  RequestAccounting,
} from '@/features/marketing/accounting'

import { DetailRow, DetailSection } from './dialogs/log-detail-layout'

const RewardDetail = lazy(() =>
  import('@/features/marketing-admin/rewards').then((module) => ({
    default: module.RewardDetail,
  }))
)

export function FundingDetails(props: {
  accounting: RequestAccounting
  admin?: boolean
}) {
  const { t, i18n } = useTranslation()
  const [sourceReward, setSourceReward] = useState<{
    kind: string
    id: number
  }>()
  const entry = props.accounting
  const amount = (money: AccountingMoney | null) =>
    money
      ? formatMarketingMoney(money.amount, money.currency, i18n.language)
      : t('Not yet known')
  let state = t('Settling')
  if (entry.state === 'settled') state = t('Settled')
  else if (entry.state === 'released') state = t('Released without consumption')
  else if (entry.state === 'review') state = t('Needs reconciliation')

  return (
    <DetailSection label={t('Consumption sources')}>
      {props.admin && (
        <>
          <DetailRow label={t('User ID')} value={entry.user_id} />
          <DetailRow
            label={t('Request ID')}
            value={entry.request_id || '—'}
            mono
          />
        </>
      )}
      <DetailRow label={t('Status')} value={state} />
      {!entry.conversion_known && (
        <p className='text-muted-foreground text-xs'>
          {t(
            'Historical currency conversion is unavailable. Amounts are not recalculated using current settings.'
          )}
        </p>
      )}
      {entry.cost_known && (
        <DetailRow label={t('Actual cost')} value={amount(entry.actual)} mono />
      )}
      <DetailRow label={t('Settled cost')} value={amount(entry.total)} mono />
      <DetailRow
        label={t('Gift amount used')}
        value={amount(entry.gift)}
        mono
      />
      <DetailRow
        label={t('Paid amount used')}
        value={amount(entry.paid)}
        mono
      />
      {entry.state !== 'settled' && entry.state !== 'released' && (
        <>
          <DetailRow
            label={t('Unsettled cost')}
            value={
              entry.cost_known ? amount(entry.unsettled) : t('Not yet known')
            }
            mono
          />
          {entry.pending_return && Number(entry.pending_return.amount) > 0 && (
            <DetailRow
              label={t('Pending source return')}
              value={amount(entry.pending_return)}
              mono
            />
          )}
          <p className='text-muted-foreground text-xs'>
            {entry.final_observed && entry.cost_known
              ? t(
                  'Final usage has been received; settlement still needs attention.'
                )
              : t(
                  'The final usage result has not been confirmed. This is not a free request.'
                )}
          </p>
        </>
      )}
      {entry.gift_sources.map((source) => (
        <div key={source.gift_id} className='border-t pt-2'>
          <DetailRow
            label={
              source.source_name ||
              t(source.source_name_key || 'Activity reward')
            }
            value={
              source.money
                ? formatMarketingMoney(
                    source.money.amount,
                    source.money.currency,
                    i18n.language
                  )
                : t('Not yet known')
            }
            mono
          />
          <DetailRow
            label={t('Validity')}
            value={
              source.expires_at
                ? formatMarketingDate(source.expires_at, i18n.language)
                : t('Never expires')
            }
          />
          {props.admin && source.reward_id && source.reward_kind && (
            <Button
              variant='link'
              size='sm'
              className='h-auto p-0'
              onClick={() => {
                if (source.reward_id && source.reward_kind) {
                  setSourceReward({
                    id: source.reward_id,
                    kind: source.reward_kind,
                  })
                }
              }}
            >
              {t('Reward source details')}
            </Button>
          )}
        </div>
      ))}
      {props.admin && (
        <DetailRow
          label={t('Accounting record')}
          value={String(entry.id)}
          mono
        />
      )}
      {props.admin && sourceReward && (
        <Suspense fallback={<LoadingState />}>
          <RewardDetail
            record={sourceReward}
            onClose={() => setSourceReward(undefined)}
          />
        </Suspense>
      )}
    </DetailSection>
  )
}
