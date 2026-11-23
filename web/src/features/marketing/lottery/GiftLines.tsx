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

import {
  formatMarketingDate,
  formatMarketingDuration,
  formatMarketingMoney,
} from '@/features/marketing-admin/format'

import type { LotteryGift } from './types'

export function GiftLines(props: { gifts: LotteryGift[]; awarded?: boolean }) {
  const { t, i18n } = useTranslation()
  return (
    <ul className='space-y-2'>
      {props.gifts.map((gift, index) => (
        <li
          // Gift items are ordered immutable snapshots without independent IDs.
          // oxlint-disable-next-line react/no-array-index-key
          key={index}
          className='flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1 text-sm'
        >
          <strong>
            {formatMarketingMoney(gift.amount, gift.currency, i18n.language)}
          </strong>
          <span>
            {gift.valid_seconds ? t('Limited-time gift') : t('Permanent gift')}
          </span>
          <span className='text-muted-foreground'>
            {gift.valid_seconds
              ? t('Validity: {{duration}}', {
                  duration: formatMarketingDuration(
                    gift.valid_seconds,
                    i18n.language
                  ),
                })
              : t('Never expires')}
          </span>
          {props.awarded && (
            <span className='text-muted-foreground w-full text-xs'>
              {gift.credited_at ? (
                <>
                  {t('Credited at')}:{' '}
                  {formatMarketingDate(gift.credited_at, i18n.language)}
                  {gift.expires_at ? (
                    <>
                      {' '}
                      · {t('Expires at')}:{' '}
                      {formatMarketingDate(gift.expires_at, i18n.language)}
                    </>
                  ) : null}
                </>
              ) : (
                t('Pending delivery')
              )}
            </span>
          )}
        </li>
      ))}
    </ul>
  )
}
