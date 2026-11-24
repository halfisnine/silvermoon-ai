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
import type { ColumnDef } from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { DataTablePage, useDataTable } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { formatMarketingDate } from '@/features/marketing-admin/format'
import { useMediaQuery } from '@/hooks'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { useAuthStore } from '@/stores/auth-store'

import { userMarketingGet, getUserActivities } from '../api'
import { ActivityCard } from '../center'
import type { Invitation } from '../types'

export function InvitationPage() {
  const userId = useAuthStore((state) => state.auth.user?.id)

  const { t, i18n } = useTranslation()
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 20 })
  const isMobile = useMediaQuery('(max-width: 640px)')
  const isDesktop = useMediaQuery('(min-width: 1024px)')
  const [rulePage, setRulePage] = useState(1)
  const query = useQuery({
    queryKey: ['marketing-user', userId, 'invitation', pagination],
    queryFn: () =>
      userMarketingGet<Invitation>('/invitation', {
        page: pagination.pageIndex + 1,
        page_size: pagination.pageSize,
      }),
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[1] === userId ? previous : undefined,
    enabled: userId != null,
  })
  const rules = useQuery({
    queryKey: ['marketing-user', userId, 'invitation-rules', rulePage],
    queryFn: () => getUserActivities(rulePage, 'invitation'),
    refetchInterval: 30000,
    enabled: userId != null,
  })
  const path = query.data?.invitation_path
  const link =
    path && path.startsWith('/') && !path.startsWith('//')
      ? `${window.location.origin}${path}`
      : ''
  const columns: ColumnDef<Invitation['friends']['items'][number]>[] = [
    { accessorKey: 'username', header: t('Username') },
    {
      accessorKey: 'joined_at',
      header: t('Registration time'),
      cell: ({ row }) =>
        formatMarketingDate(row.original.joined_at, i18n.language),
    },
  ]
  const { table } = useDataTable({
    data: query.data?.friends.items ?? [],
    columns,
    pagination,
    onPaginationChange: setPagination,
    manualPagination: true,
    enableRowSelection: false,
    totalCount: query.data?.friends.total ?? 0,
  })
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Invite for rewards')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          role='link'
          variant='outline'
          render={
            <Link
              to='/activities/rewards'
              search={{ tab: 'records', source: 'invitation' }}
            />
          }
        >
          {t('View invitation rewards')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='marketing-invitation-layout mx-auto grid w-full max-w-6xl gap-5 lg:h-[max(42rem,calc(100dvh-14rem))] lg:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]'>
          <div className='marketing-invitation-info flex min-h-0 min-w-0 flex-col gap-4'>
            {query.isPending && <LoadingState />}
            {query.data && (
              <Card className='shrink-0 gap-3 p-4'>
                <h3 className='font-semibold'>{t('Your invitation link')}</h3>
                <div className='flex gap-2'>
                  <Input
                    readOnly
                    value={link}
                    aria-label={t('Your invitation link')}
                  />
                  <CopyButton
                    value={link}
                    variant='outline'
                    aria-label={t('Copy invitation link')}
                  />
                </div>
                <p className='text-muted-foreground text-sm'>
                  {t(
                    'Inviting a friend does not guarantee a reward. The active invitation rules apply.'
                  )}
                </p>
              </Card>
            )}
            <div className='flex min-h-0 flex-1 flex-col gap-4 lg:overflow-y-auto [&>[data-slot=card]]:shrink-0'>
              {rules.isPending && <LoadingState />}
              {rules.isError && (
                <ErrorState onRetry={() => void rules.refetch()} />
              )}
              {rules.data?.items.length === 0 && (
                <Card className='marketing-invitation-empty min-h-72 flex-1 justify-center p-4'>
                  <EmptyState
                    className='min-h-0'
                    title={t('No invitation activities to display')}
                    description={t(
                      'Your invitation link, existing friends and reward history remain available.'
                    )}
                  />
                </Card>
              )}
              {rules.data?.items.map((item) => (
                <ActivityCard key={item.id} item={item} />
              ))}
              {rules.data && rules.data.total > 20 && (
                <nav
                  className='flex justify-end gap-2'
                  aria-label={t('Activity pagination')}
                >
                  <Button
                    variant='outline'
                    disabled={rulePage === 1}
                    onClick={() => setRulePage(rulePage - 1)}
                  >
                    {t('Previous')}
                  </Button>
                  <Button
                    variant='outline'
                    disabled={rulePage * 20 >= rules.data.total}
                    onClick={() => setRulePage(rulePage + 1)}
                  >
                    {t('Next')}
                  </Button>
                </nav>
              )}
            </div>
          </div>
          <Card className='marketing-invitation-friends min-h-0 min-w-0 gap-3 p-4'>
            <h3 className='shrink-0 font-semibold'>
              {t('Invited friends')}
              {query.data && (
                <>
                  {' '}
                  ·{' '}
                  {formatNumber(
                    query.data.friend_count,
                    toIntlLocale(i18n.language)
                  )}
                </>
              )}
            </h3>
            {query.isError ? (
              <ErrorState onRetry={() => void query.refetch()} />
            ) : (
              <DataTablePage
                table={table}
                columns={columns}
                toolbarProps={null}
                fixedHeight={isDesktop}
                paginationInFooter={false}
                compactPagination={isMobile}
                showPagination={
                  (query.data?.friends.total ?? 0) > pagination.pageSize ||
                  pagination.pageIndex > 0
                }
                isLoading={query.isPending}
                isFetching={query.isFetching}
                emptyTitle={t('No invited friends yet')}
              />
            )}
          </Card>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
