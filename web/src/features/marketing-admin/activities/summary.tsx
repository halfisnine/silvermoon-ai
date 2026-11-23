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
/* eslint-disable react/no-array-index-key -- Reward arrays are immutable ordered snapshots with no row identity or editable child state. */
import { useTranslation } from 'react-i18next'

import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import {
  formatMarketingDate,
  formatMarketingDuration,
  formatMarketingMoney,
} from '../format'
import type { MarketingActivity, MarketingReward } from '../types'

export function RewardSummary(props: { rewards: MarketingReward[] }) {
  const { t, i18n } = useTranslation()
  return (
    <ul className='flex flex-col gap-1'>
      {props.rewards.map((r, index) => {
        if (r.kind === 'chance') {
          return (
            <li key={index}>
              {r.pool_name || t('Selected prize pool')} ·{' '}
              {t('{{count}} draw opportunities', { count: r.count ?? 0 })}
              {r.chance_mode === 'probability' &&
                ` · ${t('Grant probability')}: ${r.probability_percent || '—'}%`}
            </li>
          )
        }
        let amount = formatMarketingMoney(
          r.amount || '0',
          r.currency || 'CNY',
          i18n.language
        )
        if (r.calculation === 'paid_ratio') {
          amount = `${r.ratio_percent || '—'}% · ${t('Percentage of credited paid amount')}`
        }
        if (r.calculation === 'cash_ratio') {
          amount = `${r.ratio_percent || '—'}% · ${t('Percentage of friend’s actual payment')}`
        }
        const validity = r.valid_seconds
          ? t('Validity: {{duration}}', {
              duration: formatMarketingDuration(r.valid_seconds, i18n.language),
            })
          : t('Permanent gift (no expiry)')
        return (
          <li key={index}>
            {amount} · {validity}
          </li>
        )
      })}
    </ul>
  )
}
export function ActivitySummary(props: { activity: MarketingActivity }) {
  const { t, i18n } = useTranslation()
  const a = props.activity
  const locale = toIntlLocale(i18n.language)
  let trigger = t('Registration succeeds')
  switch (a.condition) {
    case 'first_payment':
      trigger = t('Only the first-ever successful payment can qualify.')
      break
    case 'single_payment':
      trigger = t('Each payment is judged separately during the activity.')
      break
    case 'cumulative_payments':
      trigger = t('Payments accumulate from the start of this activity.')
      break
    case 'friend_registration':
      trigger = t('A friend registers during this activity.')
      break
    case 'friend_cumulative':
      trigger = t(
        'Each friend’s payments accumulate separately during this activity.'
      )
      break
    case 'friend_ratio':
      trigger = t('Based on the friend’s actual CNY payment, excluding gifts.')
      break
    case 'checkin':
      trigger = t('The user checks in and meets this activity’s conditions.')
      break
  }
  let frequency = t('Once per participant in this activity')
  if (a.frequency === 'every') {
    frequency = t('Every qualifying payment or cumulative threshold')
  }
  if (a.kind === 'checkin') {
    frequency = t('Once per qualifying day, plus configured streak rewards')
  }
  let eligibility = t('No recharge required')
  if (a.checkin?.condition === 'today_paid') eligibility = t('Paid today')
  if (a.checkin?.condition === 'previous_day_paid') {
    eligibility = t('Paid on the previous day')
  }
  return (
    <section aria-label={t('Rule summary')} className='flex flex-col gap-3'>
      <h3 className='font-medium'>{t('Rule summary')}</h3>
      <dl className='grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm'>
        <dt>{t('Reward trigger')}</dt>
        <dd>{trigger}</dd>
        {!!a.threshold_cny && (
          <>
            <dt>{t('Actual payment threshold (CNY)')}</dt>
            <dd>
              {formatMarketingMoney(a.threshold_cny, 'CNY', i18n.language)}
            </dd>
          </>
        )}
        <dt>{t('Reward frequency')}</dt>
        <dd>{frequency}</dd>
        <dt>{t('Activity period')}</dt>
        <dd>
          {formatMarketingDate(a.starts_at, i18n.language)} —{' '}
          {a.ends_at
            ? formatMarketingDate(a.ends_at, i18n.language)
            : t('Ongoing')}
        </dd>
        {a.exclusive_group && (
          <>
            <dt>{t('Exclusive group')}</dt>
            <dd>
              {a.exclusive_group} · {t('Priority')}:{' '}
              {formatNumber(a.priority, locale)}
            </dd>
          </>
        )}
        {a.checkin && (
          <>
            <dt>{t('Eligibility')}</dt>
            <dd>
              {eligibility}
              {a.checkin.threshold_cny && (
                <>
                  {' '}
                  ·{' '}
                  {formatMarketingMoney(
                    a.checkin.threshold_cny,
                    'CNY',
                    i18n.language
                  )}
                </>
              )}
            </dd>
            <dt>{t('Streak cycle')}</dt>
            <dd>
              {a.checkin.cycle_days
                ? t('{{days}} days per cycle', { days: a.checkin.cycle_days })
                : t('Once per milestone')}
            </dd>
            <dt>{t('Make-up check-in')}</dt>
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
        <dt>{t('Rewards')}</dt>
        <dd>
          <RewardSummary rewards={a.rewards || []} />
        </dd>
        {a.checkin?.milestones.map((m) => (
          <div key={m.days} className='contents'>
            <dt>
              {t('Consecutive days')}: {formatNumber(m.days, locale)}
            </dt>
            <dd>
              <RewardSummary rewards={m.rewards} />
            </dd>
          </div>
        ))}
      </dl>
    </section>
  )
}
