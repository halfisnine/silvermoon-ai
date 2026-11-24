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
import { CalendarCheck, Flame, Gift, RotateCcw } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Card } from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import { RewardSummary } from '@/features/marketing-admin/activities/summary'
import {
  formatMarketingDate,
  formatMarketingMoney,
} from '@/features/marketing-admin/format'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type { CheckinActivity } from '../types'

export function CheckinRuleCard(props: { activity: CheckinActivity }) {
  const { t, i18n } = useTranslation()
  const a = props.activity
  const locale = toIntlLocale(i18n.language)
  let eligibility = t('No eligible check-in activity for this date')
  if (a.today_eligibility === 'eligible') eligibility = t('Eligible today')
  if (a.today_eligibility === 'already_signed') {
    eligibility = t('Checked in today')
  }
  if (a.today_eligibility === 'payment_required') {
    eligibility = t('Payment condition not met today')
  }
  if (a.today_eligibility === 'suppressed') {
    eligibility = t(
      'Another eligible activity in the same exclusive group takes priority.'
    )
  }
  if (a.paused) eligibility = t('Paused')
  if (a.state === 'ended') eligibility = t('Ended')
  if (a.state === 'scheduled') eligibility = t('Upcoming')
  const milestones = [...a.milestones].sort(
    (left, right) => left.days - right.days
  )
  const cycleProgress =
    a.cycle_days && a.streak > 0
      ? ((a.streak - 1) % a.cycle_days) + 1
      : a.streak
  const target =
    milestones.find((milestone) => milestone.days > cycleProgress)?.days ??
    milestones.at(-1)?.days ??
    1
  return (
    <Card className='marketing-checkin-rule gap-5 p-4 sm:p-5'>
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div className='min-w-0 space-y-1.5'>
          <h3 className='flex items-center gap-2 text-base font-semibold break-words'>
            <CalendarCheck
              className='text-primary size-5 shrink-0'
              aria-hidden='true'
            />
            {a.name}
          </h3>
          <p className='text-muted-foreground text-xs leading-relaxed'>
            {formatMarketingDate(a.starts_at, i18n.language)} —{' '}
            {a.ends_at
              ? formatMarketingDate(a.ends_at, i18n.language)
              : t('Ongoing')}
          </p>
        </div>
        <Badge variant='secondary' className='max-w-full whitespace-normal'>
          {eligibility}
        </Badge>
      </div>
      <div className='grid gap-5 md:grid-cols-2'>
        <section className='min-w-0 space-y-2.5'>
          <h4 className='text-muted-foreground flex items-center gap-2 text-xs font-medium'>
            <Gift className='size-4' aria-hidden='true' />
            {t('Daily rewards')}
          </h4>
          <div className='text-sm leading-relaxed font-medium'>
            <RewardSummary rewards={a.daily_rewards} />
          </div>
        </section>
        {milestones.length > 0 && (
          <section className='min-w-0 space-y-3'>
            <div className='flex flex-wrap items-center justify-between gap-2'>
              <h4 className='text-muted-foreground flex items-center gap-2 text-xs font-medium'>
                <Flame className='text-primary size-4' aria-hidden='true' />
                {t('Current streak')}
              </h4>
              <span className='text-primary text-sm font-semibold'>
                {t('{{days}} consecutive days', {
                  days: formatNumber(a.streak, locale),
                })}
              </span>
            </div>
            <Progress
              value={Math.min(100, (cycleProgress / target) * 100)}
              aria-label={t('Current streak')}
            />
            <p className='text-muted-foreground text-xs'>
              {a.cycle_days
                ? t('{{days}} days per cycle', { days: a.cycle_days })
                : t('Once per milestone')}
            </p>
            <div className='space-y-3'>
              {milestones.map((milestone) => (
                <div
                  key={milestone.days}
                  className='marketing-checkin-milestone flex items-start gap-3 rounded-xl p-3'
                >
                  <span className='bg-primary/10 text-primary flex size-8 shrink-0 items-center justify-center rounded-full'>
                    <Gift className='size-4' aria-hidden='true' />
                  </span>
                  <div className='min-w-0 space-y-1 text-sm'>
                    <p className='font-medium'>
                      {t('Extra reward after {{days}} consecutive days', {
                        days: milestone.days,
                      })}
                    </p>
                    <div className='text-muted-foreground text-xs leading-relaxed'>
                      <RewardSummary rewards={milestone.rewards} />
                    </div>
                  </div>
                </div>
              ))}
            </div>
            <p className='text-muted-foreground text-xs leading-relaxed'>
              {t(
                'Daily and extra streak rewards are added together on a milestone day.'
              )}
            </p>
          </section>
        )}
      </div>
      <div className='border-border/60 flex flex-wrap items-center justify-between gap-3 border-t pt-3 text-xs'>
        <p className='text-muted-foreground'>
          {t('Eligibility')}:{' '}
          {a.condition === 'none' ? (
            t('No recharge required')
          ) : (
            <>
              {a.condition === 'today_paid'
                ? t('Paid today')
                : t('Paid on the previous day')}{' '}
              ·{' '}
              {formatMarketingMoney(
                a.required_payment_cny,
                'CNY',
                i18n.language
              )}
            </>
          )}
        </p>
        {a.makeup_limit > 0 && (
          <div className='space-y-1'>
            <p className='text-primary flex items-center gap-1.5 font-medium'>
              <RotateCcw className='size-3.5' aria-hidden='true' />
              {t('{{count}} make-ups remaining in this activity', {
                count: a.makeup_remaining,
              })}
            </p>
            <p className='text-muted-foreground leading-relaxed'>
              {t(
                'Previous {{days}} days; {{count}} make-ups per participant for this activity',
                { days: a.makeup_days, count: a.makeup_limit }
              )}
              {' · '}
              {a.makeup_daily
                ? t('Includes the missed daily reward')
                : t('Repairs the streak only')}
            </p>
          </div>
        )}
      </div>
    </Card>
  )
}
