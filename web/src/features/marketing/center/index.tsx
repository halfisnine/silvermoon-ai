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
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { useAuthStore } from '@/stores/auth-store'

import { getUserActivities } from '../api'
import { ActivityRules } from '../components/activity-rules'
import type { PublicActivity } from '../types'

export function ActivityCenterPage() {
  const userId = useAuthStore((state) => state.auth.user?.id)

  const { t } = useTranslation()
  const [page, setPage] = useState(1)
  const [kind, setKind] = useState('')
  const query = useQuery({
    queryKey: ['marketing-user', userId, 'activities', page, kind],
    queryFn: () => getUserActivities(page, kind),
    refetchInterval: 30000,
  })
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Activity center')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='mx-auto flex max-w-6xl flex-col gap-4'>
          <NativeSelect
            aria-label={t('Activity type')}
            value={kind}
            onChange={(e) => {
              setKind(e.target.value)
              setPage(1)
            }}
          >
            <NativeSelectOption value=''>
              {t('All activities')}
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
          </NativeSelect>
          {query.isPending && <LoadingState />}
          {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
          {query.data?.items.length === 0 && (
            <EmptyState
              title={t('No activities to display')}
              description={t(
                'Your existing gifts and draw opportunities keep their own validity. View them in Activity rewards or Lottery.'
              )}
            />
          )}
          {(['current', 'scheduled', 'ended'] as const).map((group) => {
            const items =
              query.data?.items.filter((item) =>
                group === 'current'
                  ? item.state === 'active' || item.state === 'paused'
                  : item.state === group
              ) ?? []
            if (!items.length) return null
            let heading = t('Current activities')
            if (group === 'scheduled') heading = t('Upcoming activities')
            if (group === 'ended') heading = t('Recently ended activities')
            return (
              <section
                key={group}
                className='flex flex-col gap-4'
                aria-label={heading}
              >
                <h2 className='font-semibold'>{heading}</h2>
                {items.map((item) => (
                  <ActivityCard key={item.id} item={item} />
                ))}
              </section>
            )
          })}
          <nav aria-label={t('Pagination')} className='flex justify-end gap-2'>
            <Button
              variant='outline'
              disabled={page === 1 || query.isFetching}
              onClick={() => setPage(page - 1)}
            >
              {t('Previous')}
            </Button>
            <Button
              variant='outline'
              disabled={
                !query.data || page * 20 >= query.data.total || query.isFetching
              }
              onClick={() => setPage(page + 1)}
            >
              {t('Next')}
            </Button>
          </nav>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
export function ActivityCard(props: { item: PublicActivity }) {
  const { t } = useTranslation()
  const item = props.item
  const active = item.state === 'active'
  let state = t('Ongoing')
  if (item.state === 'ended') state = t('Ended')
  if (item.state === 'scheduled') state = t('Upcoming')
  if (item.state === 'paused') state = t('Paused')
  return (
    <Card className='min-w-0 gap-4 p-4 sm:p-5'>
      <div className='flex flex-wrap items-start justify-between gap-2'>
        <h3 className='min-w-0 text-base font-semibold break-words'>
          {item.activity.name}
        </h3>
        <Badge variant='outline'>{state}</Badge>
      </div>
      <ActivityRules item={item} />
      <div className='flex flex-wrap items-center gap-x-3 gap-y-2 border-t pt-3'>
        {active && item.activity.kind === 'checkin' && (
          <Button role='link' render={<Link to='/activities/checkin' />}>
            {t('Go to check-in')}
          </Button>
        )}
        {active && item.activity.kind === 'recharge' && (
          <Button role='link' render={<Link to='/wallet' />}>
            {t('Go to wallet')}
          </Button>
        )}
        {active && item.activity.kind === 'invitation' && (
          <Button role='link' render={<Link to='/activities/invitation' />}>
            {t('Invite for rewards')}
          </Button>
        )}
        {item.state !== 'scheduled' && item.pools.length > 0 && (
          <Button
            role='link'
            variant='outline'
            render={<Link to='/activities/lottery' />}
          >
            {t('View draw opportunities')}
          </Button>
        )}
        {item.state === 'ended' && (
          <p className='text-muted-foreground text-sm'>
            {t(
              'This activity has ended. Existing rewards keep their own validity.'
            )}
          </p>
        )}
      </div>
    </Card>
  )
}
