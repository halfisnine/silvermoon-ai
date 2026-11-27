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
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'

import { marketingGet, marketingWrite } from '../api'
import {
  formatMarketingDate,
  formatMarketingDuration,
  formatMarketingMoney,
} from '../format'
import type { GiftBatch, GiftRecipient } from './types'

export function GiftBatchDetail(props: { id: number; onClose: () => void }) {
  const { t, i18n } = useTranslation()
  const client = useQueryClient()
  const query = useQuery({
    queryKey: ['marketing-admin', 'gift-batch', props.id],
    queryFn: () => marketingGet<GiftBatch>(`/gift-batches/${props.id}`),
  })
  const mutation = useMutation({
    mutationFn: () =>
      marketingWrite<GiftBatch>(`/gift-batches/${props.id}/retry`, {}),
    onSuccess: (batch) => {
      client.setQueryData(['marketing-admin', 'gift-batch', props.id], batch)
      void client.invalidateQueries({ queryKey: ['marketing-admin'] })
      void client.invalidateQueries({ queryKey: ['marketing-user'] })
    },
  })
  const batch = query.data
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !mutation.isPending) props.onClose()
      }}
      title={t('Gift issuance batch')}
      contentClassName='sm:max-w-5xl'
      footer={
        <>
          <Button
            variant='outline'
            disabled={mutation.isPending}
            onClick={props.onClose}
          >
            {t('Close')}
          </Button>
          {batch && batch.failed + batch.pending > 0 && (
            <Button
              disabled={mutation.isPending}
              onClick={() => mutation.mutate()}
            >
              {t('Retry unsuccessful recipients')}
            </Button>
          )}
        </>
      }
    >
      {query.isPending && <LoadingState />}
      {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
      {batch && (
        <div className='flex flex-col gap-4'>
          <dl className='grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm'>
            <dt>{t('Batch')}</dt>
            <dd>#{batch.id}</dd>
            <dt>{t('Operator')}</dt>
            <dd>
              {batch.actor_name} · ID {batch.actor_id}
            </dd>
            <dt>{t('Time')}</dt>
            <dd>{formatMarketingDate(batch.created_at, i18n.language)}</dd>
            <dt>{t('Gift amount per user')}</dt>
            <dd>
              {formatMarketingMoney(
                batch.amount,
                batch.currency,
                i18n.language
              )}
            </dd>
            <dt>{t('Credited total')}</dt>
            <dd>
              {formatMarketingMoney(
                batch.total_amount,
                batch.currency,
                i18n.language
              )}
            </dd>
            <dt>{t('Validity')}</dt>
            <dd>
              {batch.valid_seconds
                ? formatMarketingDuration(batch.valid_seconds, i18n.language)
                : t('Never expires')}
            </dd>
            <dt>{t('Internal issuance reason')}</dt>
            <dd className='break-words'>{batch.reason}</dd>
          </dl>
          <p role='status' className='text-sm'>
            {t(
              'Issuance results: {{credited}} credited, {{failed}} failed, {{pending}} pending',
              {
                credited: batch.credited,
                failed: batch.failed,
                pending: batch.pending,
              }
            )}
          </p>
          {mutation.isError && (
            <p role='alert' className='text-destructive text-sm'>
              {t(
                'The issuance result is unconfirmed. Retry this issuance or check the batch records before issuing again.'
              )}
            </p>
          )}
          <StaticDataTable<GiftRecipient>
            data={batch.recipients ?? []}
            getRowKey={(r) => r.id}
            tableClassName='min-w-[740px]'
            columns={[
              {
                id: 'user',
                header: t('User'),
                cell: (r) => `${r.username || '—'} · ID ${r.user_id}`,
              },
              {
                id: 'state',
                header: t('Result'),
                cell: (r) => {
                  if (r.state === 'credited') return t('Granted')
                  if (r.state === 'failed') return t('Failed')
                  return t('Pending delivery')
                },
              },
              {
                id: 'credited',
                header: t('Credited at'),
                cell: (r) =>
                  r.gift
                    ? formatMarketingDate(r.gift.credited_at, i18n.language)
                    : '—',
              },
              {
                id: 'expiry',
                header: t('Expires at'),
                cell: (r) => {
                  if (!r.gift) return '—'
                  return r.gift.expires_at
                    ? formatMarketingDate(r.gift.expires_at, i18n.language)
                    : t('Never expires')
                },
              },
              {
                id: 'remaining',
                header: t('Remaining'),
                cell: (r) =>
                  r.gift
                    ? formatMarketingMoney(
                        r.gift.remaining_amount,
                        r.gift.currency,
                        i18n.language
                      )
                    : '—',
              },
              {
                id: 'reason',
                header: t('Reason'),
                cell: (r) => (r.error_key ? t(r.error_key) : '—'),
              },
            ]}
          />
        </div>
      )}
    </Dialog>
  )
}
