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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { enUS, fr, ja, ru, vi, zhCN, zhTW } from 'react-day-picker/locale'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Turnstile } from '@/components/turnstile'
import { Button } from '@/components/ui/button'
import { Calendar } from '@/components/ui/calendar'
import { Card } from '@/components/ui/card'
import { RewardSummary } from '@/features/marketing-admin/activities/summary'
import {
  formatMarketingDate,
  formatMarketingMoney,
} from '@/features/marketing-admin/format'
import { useStatus } from '@/hooks/use-status'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import { getCalendar, submitCheckin } from '../api'
import type { CheckinActivity } from '../types'

// Calendar dates are civil dates from the server, independent of browser timezone.
function civilDate(value: string): Date {
  return new Date(`${value}T12:00:00`)
}
function civilKey(value: Date): string {
  return `${value.getFullYear()}-${String(value.getMonth() + 1).padStart(2, '0')}-${String(value.getDate()).padStart(2, '0')}`
}
export function CheckinPage() {
  const userId = useAuthStore((state) => state.auth.user?.id)

  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.language)
  const { status } = useStatus()
  const client = useQueryClient()
  const [month, setMonth] = useState('')
  const [makeup, setMakeup] = useState<string>()
  const [challenge, setChallenge] = useState<string>()
  const calendar = useQuery({
    queryKey: ['marketing-user', userId, 'checkin', month],
    queryFn: () => getCalendar(month),
    refetchInterval: 30000,
  })
  const mutate = useMutation({
    mutationFn: ({ date, token }: { date: string; token?: string }) =>
      submitCheckin(date, token),
    onSuccess: (result) => {
      setMakeup(undefined)
      setChallenge(undefined)
      toast.success(
        result.rewards_pending
          ? t(
              'Check-in recorded. Some rewards are still being delivered; view Activity rewards for their status.'
            )
          : t('Check-in recorded. View Activity rewards for the result.')
      )
      void client.invalidateQueries({ queryKey: ['marketing-user'] })
    },
    onError: (error) => {
      setChallenge(undefined)
      handleServerError(error)
    },
  })
  const begin = (date: string) => {
    if (mutate.isPending) return
    if (status?.turnstile_check && status.turnstile_site_key) {
      setMakeup(undefined)
      setChallenge(date)
      return
    }
    mutate.mutate({ date })
  }
  const data = calendar.data
  const dates = new Set(data?.activities.flatMap((a) => a.makeup_dates) ?? [])
  const signed = [
    ...new Set(
      data?.activities.flatMap((a) => a.records.map((r) => r.date)) ?? []
    ),
  ]
  const calendarLocale =
    (
      { 'zh-CN': zhCN, 'zh-TW': zhTW, fr, ja, ru, vi } as Record<
        string,
        typeof enUS
      >
    )[locale || 'en'] ?? enUS
  const dayLabel = (date: string) =>
    new Intl.DateTimeFormat(locale, {
      year: 'numeric',
      month: 'long',
      day: 'numeric',
    }).format(civilDate(date))
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Check-in')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          role='link'
          variant='outline'
          render={
            <Link
              to='/activities/rewards'
              search={{ tab: 'records', source: 'checkin' }}
            />
          }
        >
          {t('View check-in rewards')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='mx-auto flex max-w-6xl flex-col gap-4'>
          {calendar.isPending && <LoadingState />}
          {calendar.isError && (
            <ErrorState onRetry={() => void calendar.refetch()} />
          )}
          {data && (
            <>
              <Card className='gap-4 p-4'>
                <div className='flex flex-wrap items-center justify-between gap-3'>
                  <h3 className='font-semibold'>{dayLabel(data.today)}</h3>
                  <Button
                    disabled={
                      !data.can_checkin ||
                      mutate.isPending ||
                      calendar.isFetching
                    }
                    onClick={() => begin('')}
                  >
                    {data.checked_in_today
                      ? t('Checked in today')
                      : t('Check in now')}
                  </Button>
                </div>
                <p className='text-muted-foreground text-sm'>
                  {t(
                    'Rewards are granted automatically under the eligible activity rules. Choose an available past date to make up a missed check-in.'
                  )}
                </p>
                <Calendar
                  className='mx-auto max-w-full'
                  mode='single'
                  locale={calendarLocale}
                  month={civilDate(`${data.month}-01`)}
                  onMonthChange={(date) => setMonth(civilKey(date).slice(0, 7))}
                  today={civilDate(data.today)}
                  selected={makeup ? civilDate(makeup) : undefined}
                  onSelect={(date) => {
                    if (date && dates.has(civilKey(date))) {
                      setMakeup(civilKey(date))
                    }
                  }}
                  disabled={(date) =>
                    mutate.isPending || !dates.has(civilKey(date))
                  }
                  modifiers={{ signed: signed.map(civilDate) }}
                  modifiersClassNames={{
                    signed: 'text-primary font-bold underline',
                  }}
                />
                <p className='text-muted-foreground text-center text-xs'>
                  {t(
                    'Underlined dates have a check-in record. Available dates can be made up.'
                  )}
                </p>
              </Card>
              {data.activities.length === 0 && (
                <EmptyState
                  title={t('No check-in activity for this period')}
                  description={t(
                    'You can still view past rewards in Activity rewards.'
                  )}
                />
              )}
              {data.activities.map((a) => (
                <CheckinRuleCard key={a.id} activity={a} />
              ))}
            </>
          )}
        </div>
        {makeup && (
          <ConfirmDialog
            open
            onOpenChange={(open) => {
              if (!open) setMakeup(undefined)
            }}
            title={t('Make up check-in for {{date}}', {
              date: dayLabel(makeup),
            })}
            desc={t(
              'This uses one make-up from each eligible activity. Daily rewards are issued only when that activity allows them; extra streak rewards follow its rules.'
            )}
            isLoading={mutate.isPending}
            handleConfirm={() => begin(makeup)}
            confirmText={t('Make up check-in')}
          />
        )}
        {challenge !== undefined && (
          <Dialog
            open
            onOpenChange={(open) => {
              if (!open) setChallenge(undefined)
            }}
            title={t('Security verification')}
          >
            <Turnstile
              siteKey={status?.turnstile_site_key || ''}
              onVerify={(token) => {
                const date = challenge
                setChallenge(undefined)
                mutate.mutate({ date, token })
              }}
            />
          </Dialog>
        )}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
function CheckinRuleCard(props: { activity: CheckinActivity }) {
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
  return (
    <Card className='gap-3 p-4'>
      <div className='flex flex-wrap justify-between gap-2'>
        <h3 className='font-semibold break-words'>{a.name}</h3>
        <span className='text-muted-foreground text-sm'>{eligibility}</span>
      </div>
      <dl className='grid grid-cols-1 gap-2 text-sm sm:grid-cols-[auto_minmax(0,1fr)] sm:gap-x-5'>
        <dt>{t('Activity period')}</dt>
        <dd>
          {formatMarketingDate(a.starts_at, i18n.language)} —{' '}
          {a.ends_at
            ? formatMarketingDate(a.ends_at, i18n.language)
            : t('Ongoing')}
        </dd>
        <dt>{t('Eligibility')}</dt>
        <dd>
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
        </dd>
        <dt>{t('Daily rewards')}</dt>
        <dd>
          <RewardSummary rewards={a.daily_rewards} />
        </dd>
        {a.milestones.map((m) => (
          <div className='contents' key={m.days}>
            <dt>
              {t('Extra reward after {{days}} consecutive days', {
                days: m.days,
              })}
            </dt>
            <dd>
              <RewardSummary rewards={m.rewards} />
            </dd>
          </div>
        ))}
        {a.milestones.length > 0 && (
          <>
            <dt>{t('Current streak')}</dt>
            <dd>
              {t('{{days}} consecutive days', {
                days: formatNumber(a.streak, locale),
              })}{' '}
              ·{' '}
              {a.cycle_days
                ? t('{{days}} days per cycle', { days: a.cycle_days })
                : t('Once per milestone')}
              <p>
                {t(
                  'Daily and extra streak rewards are added together on a milestone day.'
                )}
              </p>
            </dd>
          </>
        )}
        {a.makeup_limit > 0 && (
          <>
            <dt>{t('Make-up check-in')}</dt>
            <dd>
              {t(
                'Previous {{days}} days; {{count}} make-ups per participant for this activity',
                { days: a.makeup_days, count: a.makeup_limit }
              )}
              <p>
                {t('{{count}} make-ups remaining in this activity', {
                  count: a.makeup_remaining,
                })}{' '}
                ·{' '}
                {a.makeup_daily
                  ? t('Includes the missed daily reward')
                  : t('Repairs the streak only')}
              </p>
            </dd>
          </>
        )}
      </dl>
    </Card>
  )
}
