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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { MultiSelect } from '@/components/multi-select'
import { Button } from '@/components/ui/button'
import { useDebounce } from '@/hooks/use-debounce'

import { marketingGet } from '../api'
import type { MarketingPage } from '../types'
import type { GiftUser } from './types'

export function GiftUserPicker(props: {
  users: GiftUser[]
  onChange: (users: GiftUser[]) => void
  disabled: boolean
}) {
  const { t } = useTranslation()
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  const keyword = useDebounce(search, 300)
  const query = useQuery({
    queryKey: ['marketing-admin', 'gift-users', keyword, page],
    queryFn: () =>
      marketingGet<MarketingPage<GiftUser>>('/gift-users', {
        keyword,
        page,
        page_size: 20,
      }),
  })
  const selected = new Set(props.users.map((u) => u.id))
  const available = new Map(
    [...props.users, ...(query.data?.items ?? [])].map((u) => [u.id, u])
  )
  const matches = new Set(query.data?.items.map((u) => String(u.id)))
  const loading = query.isFetching || search !== keyword
  let status: React.ReactNode
  if (loading) {
    status = (
      <div role='status'>
        <LoadingState inline size='sm' message={t('Loading...')} />
      </div>
    )
  } else if (query.isError) {
    status = (
      <ErrorState
        className='min-h-0 p-2'
        onRetry={() => void query.refetch()}
      />
    )
  }
  return (
    <MultiSelect
      id='gift-user-search'
      options={[...available.values()].map((u) => ({
        value: String(u.id),
        label: `${u.username} · ID ${u.id}`,
        hint: u.display_name || undefined,
        disabled: !selected.has(u.id) && selected.size >= 100,
      }))}
      selected={props.users.map((u) => String(u.id))}
      onChange={(ids) =>
        props.onChange(
          ids.flatMap((id) => {
            const user = available.get(Number(id))
            return user ? [user] : []
          })
        )
      }
      onSearchChange={(value) => {
        setSearch(value)
        setPage(1)
      }}
      filter={(id) => matches.has(id)}
      disabled={props.disabled}
      maxVisibleChips={4}
      placeholder={t('Search by username, display name or user ID')}
      emptyText={t('No results found')}
      listStatus={status}
      listFooter={
        <div className='flex items-center justify-between gap-2'>
          <span className='text-muted-foreground text-xs'>
            {t('Select up to 100 users')}
          </span>
          {query.data && query.data.total > 20 && (
            <div className='flex gap-2'>
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={props.disabled || loading || page === 1}
                onClick={() => setPage((p) => p - 1)}
              >
                {t('Previous')}
              </Button>
              <Button
                type='button'
                variant='outline'
                size='sm'
                disabled={
                  props.disabled || loading || page * 20 >= query.data.total
                }
                onClick={() => setPage((p) => p + 1)}
              >
                {t('Next')}
              </Button>
            </div>
          )}
        </div>
      }
    />
  )
}
