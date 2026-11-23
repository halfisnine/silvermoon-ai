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
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import type { ColumnDef, PaginationState } from '@tanstack/react-table'
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DataTablePage,
  DataTableRowActionMenu,
  useDataTable,
} from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DropdownMenuItem } from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { getMarketingMetadata } from '../api'
import { formatMarketingDate } from '../format'
import { getPool, getPools, poolQueryKey } from './api'
import { PoolActionDialog } from './PoolActionDialog'
import { PoolDetails } from './PoolDetails'
import { PoolEditor } from './PoolEditor'
import type { PoolAction, PoolRecord } from './types'

type DialogMode = 'create' | 'view' | 'edit' | 'copy' | PoolAction
type Selection = {
  id?: number
  mode: DialogMode
  currency: string
  moneyRevision: string
}
const noPools: PoolRecord[] = []

export function MarketingPoolsPage() {
  const { t, i18n } = useTranslation()
  const language = i18n.resolvedLanguage || i18n.language
  const locale = toIntlLocale(language)
  const [pagination, setPagination] = useState<PaginationState>({
    pageIndex: 0,
    pageSize: 20,
  })
  const [search, setSearch] = useState('')
  const [keyword, setKeyword] = useState('')
  const [selection, setSelection] = useState<Selection | null>(null)
  const metadata = useQuery({
    queryKey: ['marketing-admin', 'metadata'],
    queryFn: getMarketingMetadata,
  })
  const query = useQuery({
    queryKey: [
      ...poolQueryKey,
      'list',
      pagination.pageIndex,
      pagination.pageSize,
      keyword,
    ],
    queryFn: () =>
      getPools(pagination.pageIndex + 1, pagination.pageSize, keyword),
    placeholderData: keepPreviousData,
    refetchInterval: 30000,
  })
  const open = useCallback(
    (mode: DialogMode, record?: PoolRecord) => {
      if (
        (mode === 'create' || mode === 'edit' || mode === 'copy') &&
        !metadata.data
      ) {
        return
      }
      setSelection({
        id: record?.id,
        mode,
        currency: metadata.data?.currency ?? '',
        moneyRevision: metadata.data?.money_revision ?? '',
      })
    },
    [metadata.data]
  )
  const columns = useMemo<ColumnDef<PoolRecord>[]>(
    () => [
      {
        id: 'name',
        accessorFn: (row) => row.pool.name,
        header: t('Prize pool'),
        cell: ({ row }) => (
          <Button
            variant='link'
            className='h-auto max-w-80 justify-start p-0 text-left break-words whitespace-normal'
            onClick={() => open('view', row.original)}
          >
            {row.original.pool.name}
          </Button>
        ),
      },
      {
        id: 'state',
        header: t('Status'),
        cell: ({ row }) => <PoolStatus state={row.original.state} />,
      },
      {
        id: 'capacity',
        header: t('Remaining chance capacity'),
        cell: ({ row }) => (
          <span>
            {formatNumber(row.original.remaining_capacity, locale)} /{' '}
            {formatNumber(row.original.pool.capacity, locale)}
          </span>
        ),
      },
      {
        id: 'dates',
        header: t('Draw period'),
        cell: ({ row }) => (
          <div className='space-y-1 text-xs'>
            <p>{formatMarketingDate(row.original.pool.starts_at, language)}</p>
            <p>{formatMarketingDate(row.original.pool.ends_at, language)}</p>
          </div>
        ),
      },
      {
        id: 'actions',
        header: t('Actions'),
        cell: ({ row }) => (
          <PoolActions
            record={row.original}
            canEdit={!!metadata.data}
            onOpen={open}
          />
        ),
      },
    ],
    [t, open, locale, language, metadata.data]
  )
  const { table } = useDataTable({
    data: query.data?.items ?? noPools,
    columns,
    totalCount: query.data?.total ?? 0,
    manualPagination: true,
    manualFiltering: true,
    pagination,
    onPaginationChange: setPagination,
    getRowId: (row) => String(row.id),
    enableRowSelection: false,
  })
  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>
          {t('Prize pool management')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button
            disabled={!metadata.data || metadata.isError}
            onClick={() => open('create')}
          >
            {t('Create prize pool')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          {metadata.isError && (
            <ErrorState
              className='min-h-0 py-3'
              description={getServerErrorMessage(metadata.error)}
              onRetry={() => void metadata.refetch()}
            />
          )}
          {query.isError ? (
            <ErrorState
              description={getServerErrorMessage(query.error)}
              onRetry={() => void query.refetch()}
            />
          ) : (
            <DataTablePage
              table={table}
              columns={columns}
              isLoading={query.isLoading}
              isFetching={query.isFetching}
              emptyTitle={t('No prize pools found')}
              emptyDescription={t(
                'Create a prize pool, then award its chances through marketing activities.'
              )}
              toolbar={
                <form
                  className='flex flex-wrap items-center gap-2'
                  onSubmit={(event) => {
                    event.preventDefault()
                    setKeyword(search.trim())
                    setPagination((previous) => ({ ...previous, pageIndex: 0 }))
                  }}
                >
                  <Input
                    value={search}
                    onChange={(event) => setSearch(event.target.value)}
                    aria-label={t('Search prize pools')}
                    placeholder={t('Search prize pools')}
                    className='min-w-0 flex-1 sm:max-w-xs'
                  />
                  <Button type='submit' variant='outline'>
                    {t('Search')}
                  </Button>
                  <Button
                    type='button'
                    variant='ghost'
                    disabled={query.isFetching}
                    onClick={() => void query.refetch()}
                  >
                    {t('Refresh')}
                  </Button>
                </form>
              }
            />
          )}
        </SectionPageLayout.Content>
      </SectionPageLayout>
      {selection && (
        <PoolDialogHost
          key={`${selection.mode}:${selection.id ?? 'new'}`}
          selection={selection}
          onClose={() => setSelection(null)}
        />
      )}
    </>
  )
}
function PoolStatus(props: { state: PoolRecord['state'] }) {
  const { t } = useTranslation()
  let label = t('Scheduled')
  if (props.state === 'active') label = t('Active')
  if (props.state === 'paused') label = t('Paused')
  if (props.state === 'ended') label = t('Ended')
  return <Badge variant='outline'>{label}</Badge>
}
function PoolActions(props: {
  record: PoolRecord
  canEdit: boolean
  onOpen: (mode: DialogMode, record: PoolRecord) => void
}) {
  const { t } = useTranslation()
  const record = props.record
  return (
    <DataTableRowActionMenu
      ariaLabel={t('Actions for {{name}}', { name: record.pool.name })}
    >
      <DropdownMenuItem onClick={() => props.onOpen('view', record)}>
        {t('View details')}
      </DropdownMenuItem>
      <DropdownMenuItem
        disabled={!record.editable || !props.canEdit}
        onClick={() => props.onOpen('edit', record)}
      >
        {t('Edit')}
      </DropdownMenuItem>
      <DropdownMenuItem
        disabled={!props.canEdit}
        onClick={() => props.onOpen('copy', record)}
      >
        {t('Copy prize pool')}
      </DropdownMenuItem>
      <DropdownMenuItem
        disabled={record.state === 'ended'}
        onClick={() =>
          props.onOpen(record.state === 'paused' ? 'resume' : 'pause', record)
        }
      >
        {record.state === 'paused' ? t('Resume') : t('Pause')}
      </DropdownMenuItem>
      <DropdownMenuItem
        disabled={
          record.state === 'ended' ||
          !record.pool.prizes.some((prize) => prize.stock >= 0)
        }
        onClick={() => props.onOpen('restock', record)}
      >
        {t('Restock prize')}
      </DropdownMenuItem>
      <DropdownMenuItem
        disabled={record.state === 'ended'}
        onClick={() => props.onOpen('append', record)}
      >
        {t('Increase chance limit')}
      </DropdownMenuItem>
      <DropdownMenuItem
        disabled={record.state === 'ended'}
        onClick={() => props.onOpen('extend', record)}
      >
        {t('Extend draw deadline')}
      </DropdownMenuItem>
    </DataTableRowActionMenu>
  )
}
function PoolDialogHost(props: { selection: Selection; onClose: () => void }) {
  const { t } = useTranslation()
  const selection = props.selection
  const query = useQuery({
    queryKey: [...poolQueryKey, 'detail', selection.id],
    queryFn: () => {
      if (selection.id === undefined) {
        throw new Error('No prize pool selected')
      }
      return getPool(selection.id)
    },
    enabled: selection.id !== undefined,
    staleTime: 0,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  })
  if (selection.mode === 'create') {
    return (
      <PoolEditor
        currency={selection.currency}
        moneyRevision={selection.moneyRevision}
        onClose={props.onClose}
      />
    )
  }
  if (query.isPending || query.isFetching || query.isError || !query.data) {
    return (
      <Dialog
        open
        onOpenChange={(open) => {
          if (!open) props.onClose()
        }}
        title={t('Prize pool details')}
      >
        <div aria-live='polite'>
          {query.isError ? (
            <ErrorState
              description={getServerErrorMessage(query.error)}
              onRetry={() => void query.refetch()}
            />
          ) : (
            <LoadingState />
          )}
        </div>
      </Dialog>
    )
  }
  const record = query.data
  if (selection.mode === 'view') {
    return <PoolDetails record={record} onClose={props.onClose} />
  }
  if (selection.mode === 'edit' || selection.mode === 'copy') {
    return (
      <PoolEditor
        record={record}
        copy={selection.mode === 'copy'}
        currency={
          record.pool.prizes.flatMap((prize) => prize.gifts)[0]?.currency ??
          selection.currency
        }
        moneyRevision={record.money_revision}
        onClose={props.onClose}
      />
    )
  }
  return (
    <PoolActionDialog
      record={record}
      action={selection.mode}
      onClose={props.onClose}
    />
  )
}
