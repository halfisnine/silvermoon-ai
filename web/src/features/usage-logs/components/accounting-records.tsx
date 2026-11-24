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
import type { ColumnDef } from '@tanstack/react-table'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { marketingGet } from '@/features/marketing-admin/api'
import {
  formatMarketingDate,
  formatMarketingMoney,
} from '@/features/marketing-admin/format'
import type { MarketingPage } from '@/features/marketing-admin/types'
import type {
  AccountingCashRefund,
  AccountingPayment,
  RequestAccounting,
} from '@/features/marketing/accounting'
import { useAuthStore } from '@/stores/auth-store'

import { AccountingSourceDetails } from './accounting-source-details'
import { FundingDetails } from './funding-details'

type AccountingRow =
  | RequestAccounting
  | AccountingPayment
  | AccountingCashRefund
export function AccountingRecords(props: {
  resource: string
  filters: Record<string, string | number>
}) {
  const { t, i18n } = useTranslation()
  const actorId = useAuthStore((state) => state.auth.user?.id)
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 20 })
  const [selected, setSelected] = useState<AccountingRow>()
  const params = {
    ...props.filters,
    page: pagination.pageIndex + 1,
    page_size: pagination.pageSize,
  }
  const query = useQuery({
    queryKey: ['marketing-accounting-records', actorId, props.resource, params],
    queryFn: async () => {
      const result = await marketingGet<
        MarketingPage<AccountingRow> | AccountingRow
      >(`/${props.resource}`, params)
      if ('items' in result) return result
      return {
        items: [result],
        total: 1,
        page: 1,
        page_size: pagination.pageSize,
      }
    },
  })
  const columns: ColumnDef<AccountingRow>[] = [
    { accessorKey: 'user_id', header: t('User ID') },
    {
      id: 'date',
      header: t('Time'),
      cell: ({ row }) => {
        const entry = row.original
        let at = 0
        if ('qualified_at' in entry) at = entry.qualified_at
        else if ('posted_at' in entry) at = entry.posted_at
        else at = entry.recorded_at
        return formatMarketingDate(at, i18n.language)
      },
    },
    {
      id: 'summary',
      header: t('Details'),
      cell: ({ row }) => {
        const entry = row.original
        if ('gift_sources' in entry) {
          return (
            <span className='break-all'>
              {entry.model_name || entry.request_id || t('Model usage')}
            </span>
          )
        }
        if ('paid_cny' in entry) {
          return formatMarketingMoney(entry.paid_cny, 'CNY', i18n.language)
        }
        return formatMarketingMoney(entry.refunded_cny, 'CNY', i18n.language)
      },
    },
    {
      id: 'action',
      header: t('Source'),
      cell: ({ row }) => (
        <Button
          size='sm'
          variant='outline'
          onClick={() => setSelected(row.original)}
        >
          {t('Details')}
        </Button>
      ),
    },
  ]
  const { table } = useDataTable({
    data: query.data?.items ?? [],
    columns,
    pagination,
    onPaginationChange: setPagination,
    manualPagination: true,
    totalCount: query.data?.total ?? 0,
    enableRowSelection: false,
  })
  if (selected) {
    return (
      <div className='space-y-3 overflow-y-auto'>
        <Button
          variant='outline'
          size='sm'
          onClick={() => setSelected(undefined)}
        >
          {t('Back to results')}
        </Button>
        {'gift_sources' in selected ? (
          <FundingDetails accounting={selected} admin />
        ) : (
          <AccountingSourceDetails record={selected} />
        )}
      </div>
    )
  }
  if (query.isError) return <ErrorState onRetry={() => void query.refetch()} />
  return (
    <DataTablePage
      table={table}
      columns={columns}
      isLoading={query.isPending}
      isFetching={query.isFetching}
      emptyTitle={t('No accounting records found')}
    />
  )
}
