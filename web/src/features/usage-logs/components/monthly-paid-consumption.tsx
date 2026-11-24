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
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { formatMarketingMoney } from '@/features/marketing-admin/format'
import { getMonthlyPaidConsumption } from '@/features/marketing/accounting'
import { toIntlLocale } from '@/i18n/languages'
import { useAuthStore } from '@/stores/auth-store'

export function MonthlyPaidConsumption() {
  const { t, i18n } = useTranslation()
  const [open, setOpen] = useState(false)
  const [month, setMonth] = useState(() => {
    const now = new Date(Date.now() + 8 * 3600000)
    return new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), 1))
  })
  const userId = useAuthStore((state) => state.auth.user?.id)
  const monthKey = month.toISOString().slice(0, 7)
  const query = useQuery({
    queryKey: ['monthly-paid-consumption', userId, monthKey],
    queryFn: () => getMonthlyPaidConsumption(monthKey),
    enabled: open,
  })
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const label = new Intl.DateTimeFormat(locale, {
    year: 'numeric',
    month: 'long',
    timeZone: 'UTC',
  }).format(month)
  return (
    <Dialog
      open={open}
      onOpenChange={setOpen}
      title={t('My monthly paid consumption')}
      trigger={
        <Button variant='outline' size='sm'>
          {t('My monthly paid consumption')}
        </Button>
      }
    >
      <div className='space-y-4'>
        <div className='flex items-center justify-between gap-2'>
          <Button
            variant='outline'
            size='icon'
            aria-label={t('Previous month')}
            onClick={() =>
              setMonth(
                new Date(
                  Date.UTC(month.getUTCFullYear(), month.getUTCMonth() - 1, 1)
                )
              )
            }
          >
            <ChevronLeft />
          </Button>
          <span className='font-medium' aria-live='polite'>
            {label}
          </span>
          <Button
            variant='outline'
            size='icon'
            aria-label={t('Next month')}
            onClick={() =>
              setMonth(
                new Date(
                  Date.UTC(month.getUTCFullYear(), month.getUTCMonth() + 1, 1)
                )
              )
            }
          >
            <ChevronRight />
          </Button>
        </div>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Only settled paid consumption is counted, net of reversals to its original source. Gifts, reservations, recharges and cash refunds are excluded.'
          )}
        </p>
        {query.isPending && <LoadingState />}
        {query.isError && <ErrorState onRetry={() => void query.refetch()} />}
        {query.isSuccess && (
          <div className='space-y-2' aria-live='polite'>
            {query.data.amounts.length === 0 &&
              query.data.conversion_unknown_count === 0 && (
                <p>{t('No settled paid consumption in this month')}</p>
              )}
            {query.data.amounts.map((money) => (
              <p
                className='text-xl font-semibold break-all tabular-nums'
                key={money.currency}
              >
                {formatMarketingMoney(
                  money.amount,
                  money.currency,
                  i18n.language
                )}
              </p>
            ))}
            {query.data.conversion_unknown_count > 0 && (
              <p className='text-muted-foreground text-sm'>
                {t(
                  'Some records have no historical conversion and are excluded from these amounts.'
                )}
              </p>
            )}
          </div>
        )}
      </div>
    </Dialog>
  )
}
