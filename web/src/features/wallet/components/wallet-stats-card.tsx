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
import { Link } from '@tanstack/react-router'
import { Gift, WalletCards } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { IconBadge } from '@/components/ui/icon-badge'
import { Skeleton } from '@/components/ui/skeleton'
import { formatMarketingMoney } from '@/features/marketing-admin/format'
import type { WalletBalance } from '@/features/marketing/accounting'
import { cn } from '@/lib/utils'

export function WalletStatsCard(props: {
  balance?: WalletBalance
  loading?: boolean
  error?: boolean
  onRetry?: () => void
  compact?: boolean
}) {
  const { t, i18n } = useTranslation()
  if (props.error) {
    return <ErrorState className='min-h-28' onRetry={props.onRetry} />
  }
  const stats = [
    {
      label: t('Remaining paid amount'),
      money: props.balance?.paid,
      icon: WalletCards,
    },
    {
      label: t('Remaining gift amount'),
      money: props.balance?.gift,
      icon: Gift,
    },
  ]
  return (
    <div
      role='group'
      aria-label={t('Wallet')}
      className={cn(
        'grid grid-cols-2 divide-x rounded-lg border',
        props.compact && 'bg-background/40'
      )}
    >
      {stats.map((item, index) => (
        <div
          key={item.label}
          className={cn(
            'min-w-0',
            props.compact ? 'px-2.5 py-2.5' : 'px-3 py-3 sm:px-5 sm:py-4'
          )}
        >
          <div className='flex items-center gap-2'>
            {!props.compact && (
              <IconBadge tone={index === 0 ? 'success' : 'info'} size='stat'>
                <item.icon />
              </IconBadge>
            )}
            <span className='text-muted-foreground text-xs font-medium break-words'>
              {item.label}
            </span>
          </div>
          {props.loading ? (
            <Skeleton className='mt-2 h-7 w-full' />
          ) : (
            <div
              className={cn(
                'mt-2 font-mono text-lg font-bold break-all tabular-nums',
                !props.compact && 'sm:text-2xl'
              )}
            >
              {item.money
                ? formatMarketingMoney(
                    item.money.amount,
                    item.money.currency,
                    i18n.language
                  )
                : '—'}
            </div>
          )}
          {index === 1 && (
            <Button
              variant='link'
              size='sm'
              className='mt-1 h-auto p-0'
              render={
                <Link
                  to='/activities/rewards'
                  search={{ tab: 'gifts', source: '' }}
                />
              }
            >
              {t('Gift details')}
            </Button>
          )}
        </div>
      ))}
    </div>
  )
}
