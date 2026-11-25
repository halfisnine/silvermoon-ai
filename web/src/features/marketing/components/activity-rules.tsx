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
import { useTranslation } from 'react-i18next'

import { RewardSummary } from '@/features/marketing-admin/activities/summary'
import {
  formatMarketingDate,
  formatMarketingMoney,
} from '@/features/marketing-admin/format'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type { PublicActivity } from '../types'

export function ActivityRules(props: { item: PublicActivity }) {
  const { t, i18n } = useTranslation()
  const a = props.item.activity
  let condition = t('Registration succeeds')
  if (a.condition === 'first_payment') {
    condition = t('Only the first-ever successful payment can qualify.')
  }
  if (a.condition === 'single_payment') {
    condition = t('Each payment is judged separately during the activity.')
  }
  if (a.condition === 'cumulative_payments') {
    condition = t('Payments accumulate from the start of this activity.')
  }
  if (a.condition === 'friend_registration') {
    condition = t('A friend registers during this activity.')
  }
  if (a.condition === 'friend_cumulative') {
    condition = t(
      'Each friend’s payments accumulate separately during this activity.'
    )
  }
  if (a.condition === 'friend_ratio') {
    condition = t('Based on the friend’s actual CNY payment, excluding gifts.')
  }
  if (a.kind === 'checkin') {
    condition = t('Check in during this activity to receive eligible rewards.')
  }
  let frequency = t('Once per participant in this activity')
  if (a.kind === 'invitation' && a.frequency === 'once') {
    frequency = t('Once per invited friend in this activity')
  }
  if (a.frequency === 'every') {
    frequency = t('Every qualifying payment')
    if (
      a.condition === 'cumulative_payments' ||
      a.condition === 'friend_cumulative'
    ) {
      frequency = t('Every full accumulated threshold')
    }
    if (a.kind === 'checkin') {
      frequency = t('Once per qualifying day, plus configured streak rewards')
    }
  }
  return (
    <dl className='grid grid-cols-1 gap-2 text-sm sm:grid-cols-[minmax(7rem,auto)_minmax(0,1fr)] sm:gap-x-5'>
      <dt className='text-muted-foreground'>{t('Participation conditions')}</dt>
      <dd className='min-w-0 break-words'>
        {condition}
        {a.threshold_cny && Number(a.threshold_cny) > 0 && (
          <p>
            {t('Actual payment threshold (CNY)')}:{' '}
            {formatMarketingMoney(a.threshold_cny, 'CNY', i18n.language)}
          </p>
        )}
        {a.kind === 'invitation' && (
          <p>
            {t(
              'Only friends invited during this activity qualify. Only their payments posted during the activity count.'
            )}
          </p>
        )}
      </dd>
      <dt className='text-muted-foreground'>{t('Reward frequency')}</dt>
      <dd>{frequency}</dd>
      <dt className='text-muted-foreground'>
        {a.kind === 'checkin' ? t('Daily rewards') : t('Rewards')}
      </dt>
      <dd className='min-w-0'>
        <RewardSummary rewards={a.rewards} />
      </dd>
      {a.checkin && (
        <>
          <dt className='text-muted-foreground'>{t('Eligibility')}</dt>
          <dd>
            {a.checkin.condition === 'none' ? (
              t('No recharge required')
            ) : (
              <>
                {a.checkin.condition === 'today_paid'
                  ? t('Paid today')
                  : t('Paid on the previous day')}{' '}
                ·{' '}
                {formatMarketingMoney(
                  a.checkin.threshold_cny || '0',
                  'CNY',
                  i18n.language
                )}
              </>
            )}
          </dd>
          {a.checkin.milestones.map((m) => (
            <div key={m.days} className='contents'>
              <dt className='text-muted-foreground'>
                {t('Extra reward after {{days}} consecutive days', {
                  days: m.days,
                })}
              </dt>
              <dd>
                <RewardSummary rewards={m.rewards} />
              </dd>
            </div>
          ))}
          {a.checkin.milestones.length > 0 && (
            <>
              <dt className='text-muted-foreground'>{t('Streak cycle')}</dt>
              <dd>
                {a.checkin.cycle_days
                  ? t('{{days}} days per cycle', { days: a.checkin.cycle_days })
                  : t('Once per milestone')}
                <p>
                  {t(
                    'Daily and extra streak rewards are added together on a milestone day.'
                  )}
                </p>
              </dd>
            </>
          )}
          {a.checkin.makeup_limit > 0 && (
            <>
              <dt className='text-muted-foreground'>{t('Make-up check-in')}</dt>
              <dd>
                {t(
                  'Previous {{days}} days; {{count}} make-ups per participant for this activity',
                  { days: a.checkin.makeup_days, count: a.checkin.makeup_limit }
                )}{' '}
                ·{' '}
                {a.checkin.makeup_daily
                  ? t('Includes the missed daily reward')
                  : t('Repairs the streak only')}
              </dd>
            </>
          )}
        </>
      )}
      <dt className='text-muted-foreground'>{t('Activity period')}</dt>
      <dd>
        {formatMarketingDate(a.starts_at, i18n.language)} —{' '}
        {a.ends_at
          ? formatMarketingDate(a.ends_at, i18n.language)
          : t('Ongoing')}
      </dd>
      {props.item.pools.map((pool) => (
        <div key={pool.id} className='contents'>
          <dt className='text-muted-foreground'>{pool.name}</dt>
          <dd className='min-w-0 break-words'>
            <p>
              {t(
                'Limited opportunities: {{remaining}} remaining out of {{total}}',
                {
                  remaining: formatNumber(
                    pool.remaining_capacity,
                    toIntlLocale(i18n.language)
                  ),
                  total: formatNumber(
                    pool.capacity,
                    toIntlLocale(i18n.language)
                  ),
                }
              )}
            </p>
            <p>{t('This opportunity limit is shared by all linked activities.')}</p>
            <p>
              {t('Draw period')}:{' '}
              {formatMarketingDate(pool.starts_at, i18n.language)} —{' '}
              {formatMarketingDate(pool.ends_at, i18n.language)}
            </p>
          </dd>
        </div>
      ))}
    </dl>
  )
}
