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
import type { ColumnDef } from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataTablePage, useDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'

import {
  getActivities,
  getActivity,
  getMarketingMetadata,
  marketingWrite,
} from '../api'
import { formatMarketingDate } from '../format'
import type { ActivityDetail } from '../types'
import { ActivityEditor } from './editor'
import { activityKindLabel, activityStateLabel } from './labels'
import { ActivitySummary, RewardSummary } from './summary'

type Selection = {
  id?: number
  mode: 'view' | 'edit' | 'copy' | 'create'
  session: string
}
export function MarketingActivitiesPage() {
  const { t, i18n } = useTranslation()
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 20 })
  const [keyword, setKeyword] = useState('')
  const [kind, setKind] = useState('')
  const [selection, setSelection] = useState<Selection>()
  const query = useQuery({
    queryKey: ['marketing-admin', 'activities', pagination, keyword, kind],
    queryFn: () =>
      getActivities(
        pagination.pageIndex + 1,
        pagination.pageSize,
        keyword,
        kind
      ),
  })
  const select = (mode: Selection['mode'], id?: number) =>
    setSelection({ mode, id, session: crypto.randomUUID() })
  const columns: ColumnDef<ActivityDetail>[] = [
    {
      id: 'name',
      header: t('Activity name'),
      cell: ({ row }) => (
        <Button
          variant='link'
          onClick={() => select('view', row.original.id)}
          className='h-auto text-start whitespace-normal'
        >
          {row.original.activity.name}
        </Button>
      ),
    },
    {
      id: 'kind',
      header: t('Activity type'),
      cell: ({ row }) => activityKindLabel(row.original.activity.kind, t),
    },
    {
      id: 'state',
      header: t('Status'),
      cell: ({ row }) => (
        <Badge variant='secondary'>
          {activityStateLabel(row.original.state, t)}
        </Badge>
      ),
    },
    {
      id: 'period',
      header: t('Activity period'),
      cell: ({ row }) => (
        <div>
          {formatMarketingDate(row.original.activity.starts_at, i18n.language)}
          <br />
          {row.original.activity.ends_at
            ? formatMarketingDate(row.original.activity.ends_at, i18n.language)
            : t('Ongoing')}
        </div>
      ),
    },
    {
      id: 'rewards',
      header: t('Rewards'),
      cell: ({ row }) => (
        <RewardSummary rewards={row.original.activity.rewards} />
      ),
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) => (
        <div className='flex flex-wrap gap-2'>
          <Button
            variant='outline'
            size='sm'
            disabled={!row.original.editable}
            onClick={() => select('edit', row.original.id)}
          >
            {t('Edit')}
          </Button>
          <Button
            variant='outline'
            size='sm'
            onClick={() => select('copy', row.original.id)}
          >
            {t('Copy')}
          </Button>
        </div>
      ),
    },
  ]
  const { table } = useDataTable({
    data: query.data?.items ?? [],
    columns,
    manualPagination: true,
    manualSorting: true,
    enableRowSelection: false,
    totalCount: query.data?.total ?? 0,
    pagination,
    onPaginationChange: setPagination,
  })
  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>
          {t('Marketing activities')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button onClick={() => select('create')}>
            {t('Create activity')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          {query.isError ? (
            <ErrorState onRetry={() => void query.refetch()} />
          ) : (
            <DataTablePage
              table={table}
              columns={columns}
              isLoading={query.isPending}
              isFetching={query.isFetching}
              toolbar={
                <div className='flex flex-wrap gap-2'>
                  <Input
                    aria-label={t('Search activities')}
                    placeholder={t('Search activities')}
                    value={keyword}
                    onChange={(e) => {
                      setKeyword(e.target.value)
                      setPagination({ ...pagination, pageIndex: 0 })
                    }}
                    className='max-w-sm'
                  />
                  <NativeSelect
                    aria-label={t('Activity type')}
                    value={kind}
                    onChange={(e) => {
                      setKind(e.target.value)
                      setPagination({ ...pagination, pageIndex: 0 })
                    }}
                  >
                    <NativeSelectOption value=''>
                      {t('All types')}
                    </NativeSelectOption>
                    {['registration', 'checkin', 'recharge', 'invitation'].map(
                      (k) => (
                        <NativeSelectOption key={k} value={k}>
                          {activityKindLabel(k, t)}
                        </NativeSelectOption>
                      )
                    )}
                  </NativeSelect>
                </div>
              }
              emptyTitle={t('No activities yet')}
            />
          )}
        </SectionPageLayout.Content>
      </SectionPageLayout>
      {selection && (
        <ActivityPanel
          key={selection.session}
          selection={selection}
          onClose={() => setSelection(undefined)}
        />
      )}
    </>
  )
}
function ActivityPanel(props: { selection: Selection; onClose: () => void }) {
  const { t } = useTranslation()
  const metadata = useQuery({
    queryKey: ['marketing-admin', 'editor-metadata', props.selection.session],
    queryFn: getMarketingMetadata,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
  })
  const detail = useQuery({
    queryKey: [
      'marketing-admin',
      'activity-detail',
      props.selection.id,
      props.selection.session,
    ],
    queryFn: () => getActivity(props.selection.id ?? 0),
    enabled: !!props.selection.id,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
  })
  if (metadata.isError || detail.isError)
    {return (
      <Dialog
        open
        onOpenChange={(open) => {
          if (!open) props.onClose()
        }}
        title={t('Activity')}
      >
        <ErrorState
          onRetry={() => {
            void metadata.refetch()
            if (props.selection.id) void detail.refetch()
          }}
        />
      </Dialog>
    )}
  if (!metadata.data || (props.selection.id && !detail.data))
    {return (
      <Dialog
        open
        onOpenChange={(open) => {
          if (!open) props.onClose()
        }}
        title={t('Activity')}
      >
        <LoadingState />
      </Dialog>
    )}
  if (
    detail.data &&
    (props.selection.mode === 'view' ||
      (props.selection.mode === 'edit' && !detail.data.editable))
  )
    {return <ActivityDetailView data={detail.data} onClose={props.onClose} />}
  return (
    <ActivityEditor
      currency={metadata.data.currency}
      moneyRevision={
        detail.data?.money_revision ?? metadata.data.money_revision
      }
      original={detail.data}
      copy={props.selection.mode === 'copy'}
      onClose={props.onClose}
    />
  )
}
function ActivityDetailView(props: {
  data: ActivityDetail
  onClose: () => void
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [action, setAction] = useState<{
    name: string
    label: string
    key: string
  }>()
  const mutation = useMutation({
    mutationFn: (selected: { name: string; key: string }) =>
      marketingWrite(`/activities/${props.data.id}/${selected.name}`, {
        operation_key: selected.key,
        revision: props.data.revision,
      }),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ['marketing-admin'] })
      toast.success(t('Saved successfully'))
      props.onClose()
    },
  })
  const actions: { name: string; label: string }[] = []
  if (props.data.state === 'draft')
    {actions.push({ name: 'activate', label: t('Activate activity') })}
  else if (props.data.state === 'paused')
    {actions.push({ name: 'resume', label: t('Resume activity') })}
  else if (props.data.state !== 'ended')
    {actions.push({ name: 'pause', label: t('Pause activity') })}
  actions.push(
    props.data.activity.published
      ? { name: 'unpublish', label: t('Hide from activity center') }
      : { name: 'publish', label: t('Show in activity center') }
  )
  return (
    <>
      <Dialog
        open
        onOpenChange={(open) => {
          if (!open) props.onClose()
        }}
        title={props.data.activity.name}
        footer={
          <>
            {actions.map((a) => (
              <Button
                key={a.name}
                variant='outline'
                onClick={() => setAction({ ...a, key: crypto.randomUUID() })}
              >
                {a.label}
              </Button>
            ))}
          </>
        }
      >
        <p className='text-muted-foreground mb-4 text-sm'>
          {t(
            'Started rules cannot be edited. Copy the activity to create new rules; existing qualifications and remainders stay with the original.'
          )}
        </p>
        <ActivitySummary activity={props.data.activity} />
      </Dialog>
      <ConfirmDialog
        open={!!action}
        onOpenChange={(open) => {
          if (!open && !mutation.isPending) setAction(undefined)
        }}
        title={action?.label ?? t('Confirm')}
        desc={t(
          'This changes availability for new participation. Already earned rewards remain recorded.'
        )}
        handleConfirm={() => {
          if (action) mutation.mutate(action)
        }}
        isLoading={mutation.isPending}
      />
    </>
  )
}
