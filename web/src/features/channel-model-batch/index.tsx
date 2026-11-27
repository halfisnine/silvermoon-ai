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
import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Main } from '@/components/layout/components/main'

import '@/styles/channel-model-batch.css'
import { LoadingState } from '@/components/loading-state'
import { Combobox } from '@/components/ui/combobox'
import { getChannels } from '@/features/channels/api'
import type { Channel } from '@/features/channels/types'
import { requireServerSuccess } from '@/lib/server-error-message'

import { BatchWorkspace } from './workspace'

const route = getRouteApi('/_authenticated/channel-model-batch/')

export function ChannelModelBatch() {
  const { t } = useTranslation()
  const search = route.useSearch()
  const navigate = route.useNavigate()
  const channels = useQuery({
    queryKey: ['batch-channel-list'],
    queryFn: async () => {
      const items: Channel[] = []
      for (let page = 1; ; page++) {
        const response = requireServerSuccess(
          await getChannels({ p: page, page_size: 100 })
        )
        const data = response.data
        if (!data) throw new Error(t('Failed to load channels'))
        items.push(...data.items)
        if (items.length >= data.total || data.items.length === 0) break
      }
      return items
    },
  })
  const picker = channels.isError ? (
    <ErrorState
      description={channels.error.message}
      onRetry={() => void channels.refetch()}
    />
  ) : (
    <Combobox
      aria-label={t('Select channel')}
      disabled={channels.isPending}
      value={search.channel ? String(search.channel) : ''}
      placeholder={t('Select channel')}
      searchPlaceholder={t('Search channels')}
      options={(channels.data ?? []).map((channel) => ({
        value: String(channel.id),
        label: `#${channel.id} · ${channel.name}`,
      }))}
      onValueChange={(value) => {
        const id = Number(value)
        // Router navigation invokes the workspace's unsaved-change guard.
        void navigate({ search: { channel: id } })
      }}
    />
  )
  return (
    <Main className='channel-model-batch'>
      <h1 className='sr-only'>{t('Batch pricing')}</h1>
      <div className='batch-page'>
        {channels.isPending && <LoadingState />}
        {search.channel ? (
          <BatchWorkspace
            key={search.channel}
            channelId={search.channel}
            channelPicker={picker}
          />
        ) : (
          <>
            <section className='batch-overview'>{picker}</section>
            <EmptyState title={t('Select a channel to begin')} />
          </>
        )}
      </div>
    </Main>
  )
}
