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
import { toIntlLocale } from '@/i18n/languages'
import { formatExplicitCurrencyAmount } from '@/lib/currency'

/** API amounts have already been converted. Never apply the site's rate twice. */
export function formatMarketingMoney(
  amount: string,
  currency: string,
  language: string
): string {
  return formatExplicitCurrencyAmount(amount, currency, toIntlLocale(language))
}
export function formatMarketingDate(
  timestamp: number,
  language: string
): string {
  if (!timestamp) return '—'
  return new Intl.DateTimeFormat(toIntlLocale(language), {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
    timeZone: 'Asia/Shanghai',
  }).format(new Date(timestamp * 1000))
}

/** Pick an exact unit; a short gift must never be described as valid for zero hours. */
export function formatMarketingDuration(
  seconds: number,
  language: string
): string {
  let unit = 'second'
  let value = seconds
  if (seconds % 86400 === 0) {
    unit = 'day'
    value = seconds / 86400
  } else if (seconds % 3600 === 0) {
    unit = 'hour'
    value = seconds / 3600
  } else if (seconds % 60 === 0) {
    unit = 'minute'
    value = seconds / 60
  }
  // The shared numeric formatter does not format localized duration units.
  return new Intl.NumberFormat(toIntlLocale(language), {
    style: 'unit',
    unit,
    unitDisplay: 'long',
    maximumFractionDigits: 0,
  }).format(value)
}
