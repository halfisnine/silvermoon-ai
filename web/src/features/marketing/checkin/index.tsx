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
import { useStatus } from '@/hooks/use-status'
import { toIntlLocale } from '@/i18n/languages'
import { handleServerError } from '@/lib/handle-server-error'
import { useAuthStore } from '@/stores/auth-store'

import { getCalendar, submitCheckin } from '../api'
import { CheckinCalendarView } from './calendar'
import { civilDate } from './dates'
import { CheckinRuleCard } from './rule-card'

import './checkin.css'

export function CheckinPage() {
  const userId = useAuthStore((state) => state.auth.user?.id)

  const { t, i18n } = useTranslation()
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
  const dayLabel = (date: string) =>
    new Intl.DateTimeFormat(toIntlLocale(i18n.language), {
      year: 'numeric',
      month: 'long',
      day: 'numeric',
    }).format(civilDate(date))
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Check-in for rewards')}
      </SectionPageLayout.Title>
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
        <div className='marketing-checkin mx-auto flex w-full max-w-5xl flex-col gap-5 pb-6'>
          {calendar.isPending && <LoadingState />}
          {calendar.isError && (
            <ErrorState onRetry={() => void calendar.refetch()} />
          )}
          {data && (
            <>
              {data.activities.map((activity) => (
                <CheckinRuleCard key={activity.id} activity={activity} />
              ))}
              <CheckinCalendarView
                data={data}
                busy={mutate.isPending || calendar.isFetching}
                makeup={makeup}
                onMonthChange={setMonth}
                onCheckin={() => begin('')}
                onMakeup={setMakeup}
              />
              {data.activities.length === 0 && (
                <EmptyState
                  title={t('No check-in activity for this period')}
                  description={t(
                    'You can still view past rewards in Activity rewards.'
                  )}
                />
              )}
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
