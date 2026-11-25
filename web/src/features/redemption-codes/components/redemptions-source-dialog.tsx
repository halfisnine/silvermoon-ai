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

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { createServerError } from '@/lib/server-error-message'

import { getRedemption } from '../api'
import { formatRedemptionMoney, formatRedemptionDate } from '../lib/display'

export function RedemptionsSourceDialog(props: {
  id: number
  onClose: () => void
}) {
  const { t, i18n } = useTranslation()
  const detail = useQuery({
    queryKey: ['redemption-detail', props.id],
    retry: false,
    queryFn: async () => {
      const response = await getRedemption(props.id)
      if (!response.success || !response.data)
        {throw createServerError(response, t('Failed to load'))}
      return response.data
    },
  })
  const row = detail.data
  const fields = row
    ? [
        [t('Name'), row.name],
        [
          t('Actual payment (CNY)'),
          formatRedemptionMoney(row.paid_amount_cny, 'CNY', i18n.language),
        ],
        [
          t('Credited paid amount'),
          formatRedemptionMoney(
            row.credit_amount,
            row.credit_currency,
            i18n.language
          ),
        ],
        [
          t('Recorded credit at creation'),
          row.sale_registered
            ? formatRedemptionMoney(
                row.original_credit_amount,
                row.original_credit_currency,
                i18n.language
              )
            : t('Sale details require verification'),
        ],
        [t('Sales channel'), row.sales_channel],
        [t('Sales batch'), row.sales_batch],
        [t('Sales evidence (optional)'), row.sales_evidence || '—'],
        [t('Created'), formatRedemptionDate(row.created_time, i18n.language)],
        [
          t('Code validity'),
          row.expired_time
            ? formatRedemptionDate(row.expired_time, i18n.language)
            : t('Never expires'),
        ],
        [
          t('Redeemed By'),
          row.redeemed
            ? row.used_user_name || t('Deleted user')
            : t('Not redeemed'),
        ],
        [t('Redeemed'), formatRedemptionDate(row.redeemed_time, i18n.language)],
      ]
    : []
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Sale details')}
      description={t(
        'Actual sale proceeds and wallet credit are separate records.'
      )}
      contentClassName='sm:max-w-xl'
    >
      {detail.isLoading && <LoadingState />}
      {detail.isError && (
        <ErrorState
          title={t('Failed to load')}
          onRetry={() => {
            void detail.refetch()
          }}
        />
      )}
      {row && !detail.isError && (
        <>
          <dl className='space-y-3'>
            {fields.map(([label, value]) => (
              <div key={label} className='grid gap-1 sm:grid-cols-2'>
                <dt className='text-muted-foreground'>{label}</dt>
                <dd className='[overflow-wrap:anywhere] break-words'>
                  {value}
                </dd>
              </div>
            ))}
          </dl>
          <CopyButton
            value={row.key}
            variant='outline'
            size='sm'
            className='mt-4'
            aria-label={t('Copy redemption code')}
          >
            {t('Copy code')}
          </CopyButton>
          {row.redeemed && (
            <p className='text-muted-foreground mt-3 text-sm'>
              {t('Used redemption codes cannot be changed')}
            </p>
          )}
        </>
      )}
    </Dialog>
  )
}
