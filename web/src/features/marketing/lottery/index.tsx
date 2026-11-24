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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { formatMarketingDate } from '@/features/marketing-admin/format'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import { getLotteryPool, getLotteryPools } from './api'
import { DrawStage } from './DrawStage'
import { GiftLines } from './GiftLines'
import type { LotteryPool } from './types'
import { useLotteryDraw } from './use-draw'

export function LotteryPage() {
  const { t } = useTranslation()
  const userId = useAuthStore((state) => state.auth.user?.id)
  const [selected, setSelected] = useState<number>()
  const query = useQuery({
    queryKey: ['marketing', 'lottery', userId, 'options'],
    queryFn: getLotteryPools,
    enabled: !!userId,
    refetchInterval: 30000,
  })
  const poolId = query.data?.some((pool) => pool.id === selected)
    ? selected
    : query.data?.[0]?.id
  let content
  if (!userId) {
    content = <ErrorState />
  } else if (query.isPending) {
    content = <LoadingState />
  } else if (query.isError) {
    content = <ErrorState onRetry={() => void query.refetch()} />
  } else if (!query.data.length) {
    content = (
      <EmptyState
        title={t('No prize pools available')}
        description={t(
          'Your available prize pools and chances will appear here.'
        )}
      />
    )
  } else {
    content = (
      <div className='mx-auto w-full max-w-6xl space-y-6 pb-6'>
        <div className='flex flex-wrap items-end gap-3'>
          <div className='min-w-0 flex-1 space-y-2'>
            <Label htmlFor='lottery-pool'>{t('Prize pool')}</Label>
            <NativeSelect
              id='lottery-pool'
              value={poolId}
              onChange={(e) => setSelected(Number(e.target.value))}
              className='w-full'
            >
              {query.data.map((pool) => (
                <NativeSelectOption key={pool.id} value={pool.id}>
                  {pool.name}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </div>
        </div>
        {poolId && (
          <PoolExperience
            key={`${userId}:${poolId}`}
            userId={userId}
            poolId={poolId}
          />
        )}
      </div>
    )
  }
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Lottery')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>{content}</SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

function PoolExperience(props: { userId: number; poolId: number }) {
  const { t, i18n } = useTranslation()
  const query = useQuery({
    queryKey: ['marketing', 'lottery', props.userId, 'detail', props.poolId],
    queryFn: () => getLotteryPool(props.poolId),
    refetchInterval: 15000,
  })
  const draw = useLotteryDraw(props.userId, props.poolId)
  const pool = query.data
  if (query.isPending) return <LoadingState />
  if (query.isError || !pool) {
    return <ErrorState onRetry={() => void query.refetch()} />
  }
  const blocked =
    !pool.can_draw ||
    draw.busy ||
    draw.recovering ||
    !!draw.result ||
    draw.pendingKey
  return (
    <>
      <header className='flex flex-wrap items-start justify-between gap-3'>
        <div className='min-w-0 space-y-1'>
          <h2 className='text-xl font-semibold break-words'>{pool.name}</h2>
          <p className='text-muted-foreground text-sm'>
            {formatMarketingDate(pool.starts_at, i18n.language)} —{' '}
            {formatMarketingDate(pool.ends_at, i18n.language)}
          </p>
        </div>
        <p>
          {t('Available chances')}:{' '}
          <strong>
            {formatNumber(pool.available_chances, toIntlLocale(i18n.language))}
          </strong>
        </p>
      </header>
      <p role='status' className='text-sm'>
        {poolState(pool, t)}
      </p>
      {draw.recovering && (
        <LoadingState inline message={t('Recovering your draw result…')} />
      )}
      {draw.error && (
        <ErrorState
          description={getServerErrorMessage(draw.error)}
          onRetry={draw.refresh}
        />
      )}
      {draw.pendingKey && !draw.result && !draw.recovering && !draw.busy && (
        <div className='flex flex-wrap items-center gap-3'>
          <p className='text-sm'>
            {t(
              'This attempt has no confirmed result yet. Retry the same attempt without starting a new draw.'
            )}
          </p>
          <Button onClick={draw.start}>{t('Retry this draw')}</Button>
        </div>
      )}
      <DrawStage
        pool={pool}
        result={draw.result}
        disabled={blocked}
        busy={draw.busy}
        onDraw={draw.start}
        onCloseResult={draw.acknowledge}
      />
      <section className='space-y-3' aria-label={t('Prize list')}>
        <h3 className='font-semibold'>{t('Prize list')}</h3>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Combination gifts are awarded together. Sold-out prizes cannot be drawn.'
          )}
        </p>
        <div className='grid auto-rows-fr gap-3 sm:grid-cols-2 xl:grid-cols-3'>
          {pool.prizes.map((prize) => (
            <article
              key={prize.id}
              className='silvermoon-lottery-prize flex min-w-0 flex-col gap-3 rounded-xl p-4'
            >
              <div className='flex items-start gap-2'>
                <h4 className='min-w-0 flex-1 font-semibold break-words'>
                  {prize.name}
                </h4>
                {!prize.available && (
                  <Badge variant='secondary'>
                    {prize.state === 'sold_out'
                      ? t('Sold out')
                      : t('Unavailable')}
                  </Badge>
                )}
              </div>
              {prize.no_prize ? (
                <p className='text-sm'>
                  {t('No gift is awarded for this result.')}
                </p>
              ) : (
                <GiftLines gifts={prize.gifts} />
              )}
            </article>
          ))}
        </div>
      </section>
      <section className='space-y-3' aria-label={t('Chance sources')}>
        <h3 className='font-semibold'>{t('Chance sources')}</h3>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Chances keep their own deadlines when their source activity ends. Pausing a pool does not pause expiry.'
          )}
        </p>
        <ul className='divide-y'>
          {pool.chances.map((chance) => (
            <li
              key={chance.id}
              className='flex flex-wrap justify-between gap-2 py-3 text-sm'
            >
              <span className='min-w-0 break-words'>{chance.source_name}</span>
              <span>
                {formatNumber(chance.remaining, toIntlLocale(i18n.language))} ·{' '}
                {
                  {
                    expired: t('Expired'),
                    cleared: t('Cleared after refund'),
                    used: t('Used'),
                    ready: t('Available'),
                  }[chance.state]
                }{' '}
                · {t('Expires at')}:{' '}
                {formatMarketingDate(chance.expires_at, i18n.language)}
              </span>
            </li>
          ))}
        </ul>
      </section>
    </>
  )
}
function poolState(pool: LotteryPool, t: (key: string) => string) {
  if (pool.state === 'paused') {
    return t(
      'Draws are paused. Existing chances continue to expire at their original deadlines.'
    )
  }
  if (pool.state === 'scheduled') {
    return t('This prize pool has not opened yet.')
  }
  if (pool.state === 'ended') return t('This prize pool has ended.')
  if (!pool.available_chances) {
    return t('You have no available chances for this pool.')
  }
  if (!pool.can_draw) {
    return t('Drawing is temporarily unavailable. Refresh to check again.')
  }
  return t('Use one of your available chances to draw.')
}
