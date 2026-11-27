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
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'

import { marketingGet } from '../api'
import { formatMarketingDate, formatMarketingMoney } from '../format'
import { giftState } from './labels'
import type { AdminGift } from './types'

export function AdminGiftDetail(props: {
  id: number
  onClose: () => void
  onBatch: (id: number) => void
}) {
  const { t, i18n } = useTranslation()
  const query = useQuery({
    queryKey: ['marketing-admin', 'gift-detail', props.id],
    queryFn: () => marketingGet<AdminGift>(`/gifts/${props.id}`),
  })
  const g = query.data
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Gift details')}
    >
      {query.isPending && <LoadingState />}
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      {g && (
        <div className='flex flex-col gap-4 text-sm'>
          <dl className='grid grid-cols-[auto_1fr] gap-x-4 gap-y-2'>
            <dt>{t('User')}</dt>
            <dd>
              {g.username || '—'} · ID {g.user_id}
            </dd>
            <dt>{t('Source')}</dt>
            <dd>{g.source_name_key ? t(g.source_name_key) : g.source_name}</dd>
            <dt>{t('Status')}</dt>
            <dd>{giftState(g.state, t)}</dd>
            <dt>{t('Credited at')}</dt>
            <dd>{formatMarketingDate(g.credited_at, i18n.language)}</dd>
            <dt>{t('Expires at')}</dt>
            <dd>
              {g.expires_at
                ? formatMarketingDate(g.expires_at, i18n.language)
                : t('Never expires')}
            </dd>
            {(
              [
                'granted_amount',
                'consumed_amount',
                'remaining_amount',
                'expired_amount',
                'cleared_amount',
              ] as const
            ).map((key, index) => (
              <div className='contents' key={key}>
                <dt>
                  {
                    [
                      t('Gift received'),
                      t('Consumed'),
                      t('Remaining'),
                      t('Expired amount'),
                      t('Cleared amount'),
                    ][index]
                  }
                </dt>
                <dd>
                  {formatMarketingMoney(g[key], g.currency, i18n.language)}
                </dd>
              </div>
            ))}
          </dl>
          {!!g.batch_id && (
            <Button
              variant='outline'
              onClick={() => g.batch_id && props.onBatch(g.batch_id)}
            >
              {t('View issuance batch')}
            </Button>
          )}
        </div>
      )}
    </Dialog>
  )
}
