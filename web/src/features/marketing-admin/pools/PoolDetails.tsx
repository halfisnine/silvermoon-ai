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
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import {
  formatMarketingDate,
  formatMarketingDuration,
  formatMarketingMoney,
} from '../format'
import type { PoolRecord } from './types'

export function PoolDetails(props: {
  record: PoolRecord
  onClose: () => void
}) {
  const { t, i18n } = useTranslation()
  const language = i18n.resolvedLanguage || i18n.language
  const locale = toIntlLocale(language)
  const record = props.record
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={record.pool.name}
      contentClassName='sm:max-w-3xl'
      contentHeight='min(65vh, 700px)'
      footer={<Button onClick={props.onClose}>{t('Close')}</Button>}
    >
      <dl className='grid grid-cols-1 gap-x-4 gap-y-2 text-sm sm:grid-cols-[auto_1fr]'>
        <dt>{t('Draws open at')}</dt>
        <dd>{formatMarketingDate(record.pool.starts_at, language)}</dd>
        <dt>{t('Draws close at')}</dt>
        <dd>{formatMarketingDate(record.pool.ends_at, language)}</dd>
        <dt>{t('Total chance limit')}</dt>
        <dd>{formatNumber(record.pool.capacity, locale)}</dd>
        <dt>{t('Issued chances')}</dt>
        <dd>{formatNumber(record.issued, locale)}</dd>
        <dt>{t('Remaining chance capacity')}</dt>
        <dd>{formatNumber(record.remaining_capacity, locale)}</dd>
      </dl>
      <div className='mt-5 divide-y'>
        {record.pool.prizes.map((prize, index) => (
          <section key={prize.id ?? index} className='space-y-2 py-4'>
            <h3 className='font-semibold break-words'>{prize.name}</h3>
            <p className='text-muted-foreground text-sm'>
              {t('Weight')}: {formatNumber(prize.weight, locale)} ·{' '}
              {t('Prize stock')}:{' '}
              {prize.stock === -1
                ? t('Unlimited stock')
                : formatNumber(prize.stock, locale)}
            </p>
            {prize.gifts.length === 0 && (
              <p className='text-sm'>{t('No prize')}</p>
            )}
            {prize.gifts.map((gift, giftIndex) => {
              let validity = t('Never expires')
              if (gift.valid_seconds > 0) {
                const duration = formatMarketingDuration(
                  gift.valid_seconds,
                  i18n.language
                )
                validity = t('Validity: {{duration}}', { duration })
              }
              return (
                <p
                  // Server gift entries have no IDs; this read-only snapshot preserves their order.
                  // oxlint-disable-next-line react/no-array-index-key
                  key={giftIndex}
                  className='flex flex-wrap gap-x-2 gap-y-1 text-sm'
                >
                  <span>
                    {gift.valid_seconds === 0
                      ? t('Permanent gift')
                      : t('Limited-time gift')}
                  </span>
                  <strong>
                    {formatMarketingMoney(gift.amount, gift.currency, language)}
                  </strong>
                  <span className='text-muted-foreground'>{validity}</span>
                </p>
              )
            })}
          </section>
        ))}
      </div>
    </Dialog>
  )
}
