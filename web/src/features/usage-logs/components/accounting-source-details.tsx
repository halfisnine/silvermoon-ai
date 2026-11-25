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
  formatMarketingMoney,
} from '@/features/marketing-admin/format'
import type {
  AccountingCashRefund,
  AccountingMoney,
  AccountingPayment,
} from '@/features/marketing/accounting'

import { DetailRow, DetailSection } from './dialogs/log-detail-layout'

export function AccountingSourceDetails(props: {
  record: AccountingPayment | AccountingCashRefund
}) {
  const { t, i18n } = useTranslation()
  const entry = props.record
  const money = (value: AccountingMoney | null) =>
    value
      ? formatMarketingMoney(value.amount, value.currency, i18n.language)
      : t('Amount unavailable')
  if ('paid_cny' in entry) {
    return (
      <DetailSection label={t('Verified payment')}>
        <DetailRow label={t('User ID')} value={entry.user_id} />
        <DetailRow
          label={t('Time')}
          value={formatMarketingDate(entry.posted_at, i18n.language)}
        />
        <DetailRow
          label={t('Actual paid CNY')}
          value={formatMarketingMoney(entry.paid_cny, 'CNY', i18n.language)}
          mono
        />
        <DetailRow
          label={t('Credited paid amount')}
          value={money(entry.credited)}
          mono
        />
        {entry.order_reference && (
          <DetailRow label={t('Order')} value={entry.order_reference} mono />
        )}
        {entry.provider && (
          <DetailRow label={t('Payment provider')} value={entry.provider} />
        )}
        {entry.redemption_id && (
          <DetailRow
            label={t('Paid code')}
            value={entry.redemption_name || String(entry.redemption_id)}
          />
        )}
        {entry.sales_channel && (
          <DetailRow label={t('Sales channel')} value={entry.sales_channel} />
        )}
        {entry.sales_batch && (
          <DetailRow label={t('Sales batch')} value={entry.sales_batch} />
        )}
        {entry.fact_id && (
          <DetailRow label={t('Payment event')} value={entry.fact_id} mono />
        )}
        <p className='text-muted-foreground text-xs'>
          {t(
            'Payment provenance does not allocate each request to an individual recharge order.'
          )}
        </p>
      </DetailSection>
    )
  }
  return (
    <DetailSection label={t('Cash refund')}>
      <DetailRow label={t('User ID')} value={entry.user_id} />
      <DetailRow
        label={t('Time')}
        value={formatMarketingDate(entry.recorded_at, i18n.language)}
      />
      <DetailRow
        label={t('Verified refundable amount')}
        value={formatMarketingMoney(
          entry.exact_refund_cny,
          'CNY',
          i18n.language
        )}
        mono
      />
      <DetailRow
        label={t('Cash refunded')}
        value={formatMarketingMoney(entry.refunded_cny, 'CNY', i18n.language)}
        mono
      />
      <DetailRow
        label={t('Rounding difference')}
        value={formatMarketingMoney(
          entry.rounding_difference_cny,
          'CNY',
          i18n.language
        )}
        mono
      />
      <DetailRow
        label={t('Cleared paid amount')}
        value={money(entry.cleared_paid)}
        mono
      />
      <DetailRow
        label={t('Cleared gift amount')}
        value={money(entry.cleared_gift)}
        mono
      />
      <p className='text-muted-foreground text-xs'>
        {t(
          'Recorded after a manual cash refund. This is separate from task cost returns.'
        )}
      </p>
    </DetailSection>
  )
}
