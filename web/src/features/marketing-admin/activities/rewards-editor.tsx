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
import {
  useFormContext,
  useWatch,
  useFieldArray,
  type FieldPath,
} from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { FieldGroup, FieldSet, FieldLegend } from '@/components/ui/field'
import { FormField, FormItem, FormMessage } from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'

import { getPoolOptions } from '../api'
import type { MarketingActivity, MarketingReward } from '../types'
import { ActivityField, GiftValidityField } from './fields'
import { defaultReward } from './schema'

type RewardPath = 'rewards' | `checkin.milestones.${number}.rewards`
export function RewardsEditor(props: {
  name: RewardPath
  currency: string
  title: string
}) {
  const { t } = useTranslation()
  const form = useFormContext<MarketingActivity>()
  const rows = useFieldArray({ control: form.control, name: props.name })
  const condition = useWatch({ control: form.control, name: 'condition' })
  const addGift = () =>
    rows.append(
      condition === 'friend_ratio'
        ? {
            kind: 'gift',
            calculation: 'cash_ratio',
            ratio_percent: '',
            currency: props.currency,
            valid_seconds: 0,
          }
        : defaultReward(props.currency)
    )
  return (
    <FieldSet>
      <FieldLegend>{props.title}</FieldLegend>
      {rows.fields.map((row, index) => (
        <RewardRow
          key={row.id}
          name={`${props.name}.${index}`}
          currency={props.currency}
          onRemove={() => rows.remove(index)}
        />
      ))}
      <FormField
        control={form.control}
        name={props.name}
        render={() => (
          <FormItem>
            <FormMessage />
          </FormItem>
        )}
      />
      <div className='flex flex-wrap gap-2'>
        <Button
          type='button'
          variant='outline'
          onClick={addGift}
          disabled={rows.fields.length >= 20}
        >
          {t('Add gift')}
        </Button>
        {condition !== 'friend_ratio' && (
          <Button
            type='button'
            variant='outline'
            onClick={() =>
              rows.append({
                kind: 'chance',
                pool_id: 0,
                count: 1,
                chance_mode: 'direct',
              })
            }
            disabled={rows.fields.length >= 20}
          >
            {t('Add draw opportunities')}
          </Button>
        )}
      </div>
    </FieldSet>
  )
}
function RewardRow(props: {
  name: `${RewardPath}.${number}`
  currency: string
  onRemove: () => void
}) {
  const { t } = useTranslation()
  const form = useFormContext<MarketingActivity>()
  const reward = useWatch({
    control: form.control,
    name: props.name,
  }) as MarketingReward
  const kind = useWatch({ control: form.control, name: 'kind' })
  const condition = useWatch({ control: form.control, name: 'condition' })
  const field = (suffix: keyof MarketingReward) =>
    `${props.name}.${suffix}` as FieldPath<MarketingActivity>
  const canRatio = kind === 'recharge' && condition !== 'cumulative_payments'
  const canProbability =
    kind === 'recharge' && condition === 'cumulative_payments'
  return (
    <>
      <FieldGroup className='grid items-start gap-3 sm:grid-cols-2 lg:grid-cols-3'>
        {reward.kind === 'gift' ? (
          <>
            {canRatio && (
              <ActivityField
                name={field('calculation')}
                label={t('Gift calculation')}
                options={[
                  { value: 'fixed', label: t('Fixed gift') },
                  {
                    value: 'paid_ratio',
                    label: t('Percentage of credited paid amount'),
                  },
                ]}
                onChange={(v) =>
                  form.setValue(props.name, {
                    kind: 'gift',
                    calculation: v as 'fixed' | 'paid_ratio',
                    currency: props.currency,
                    valid_seconds: reward.valid_seconds,
                    ...(v === 'fixed' ? { amount: '' } : { ratio_percent: '' }),
                  })
                }
              />
            )}
            {reward.calculation === 'fixed' ? (
              <ActivityField
                name={field('amount')}
                label={t('Gift amount ({{currency}})', {
                  currency: props.currency,
                })}
              />
            ) : (
              <ActivityField
                name={field('ratio_percent')}
                label={t('Gift percentage (%)')}
                description={
                  condition === 'friend_ratio'
                    ? t(
                        'Based on the friend’s actual CNY payment, excluding gifts.'
                      )
                    : t(
                        'Based on this payment’s credited paid amount, excluding gifts.'
                      )
                }
              />
            )}
            <GiftValidityField name={field('valid_seconds')} />
          </>
        ) : (
          <>
            <PoolField name={field('pool_id')} selected={reward} />
            <ActivityField
              name={field('count')}
              type='number'
              label={t('Draw opportunities per reward')}
            />
            {canProbability && (
              <ActivityField
                name={field('chance_mode')}
                label={t('Opportunity grant mode')}
                options={[
                  { value: 'direct', label: t('Grant directly') },
                  { value: 'probability', label: t('Grant by probability') },
                ]}
                onChange={(v) =>
                  form.setValue(props.name, {
                    kind: 'chance',
                    pool_id: reward.pool_id,
                    pool_name: reward.pool_name,
                    count: reward.count,
                    chance_mode: v as 'direct' | 'probability',
                    ...(v === 'probability' ? { probability_percent: '' } : {}),
                  })
                }
              />
            )}
            {reward.chance_mode === 'probability' && (
              <ActivityField
                name={field('probability_percent')}
                label={t('Chance of receiving opportunities (%)')}
              />
            )}
          </>
        )}
        <Button
          type='button'
          variant='outline'
          className='sm:mt-5.5'
          onClick={props.onRemove}
        >
          {t('Remove reward')}
        </Button>
      </FieldGroup>
      <Separator />
    </>
  )
}
function PoolField(props: {
  name: FieldPath<MarketingActivity>
  selected: MarketingReward
}) {
  const { t } = useTranslation()
  const form = useFormContext<MarketingActivity>()
  const [page, setPage] = useState(1)
  const [keyword, setKeyword] = useState('')
  const pools = useQuery({
    queryKey: ['marketing-admin', 'pool-options', page, keyword],
    queryFn: () => getPoolOptions(page, keyword),
  })
  const options = (pools.data?.items ?? []).map((p) => ({
    value: String(p.id),
    label: p.pool.name,
  }))
  if (
    props.selected.pool_id &&
    !options.some((o) => o.value === String(props.selected.pool_id))
  ) {
    options.unshift({
      value: String(props.selected.pool_id),
      label: props.selected.pool_name || t('Selected prize pool'),
    })
  }
  options.unshift({ value: '0', label: t('Select a prize pool') })
  return (
    <div className='flex min-w-0 flex-col gap-2'>
      <ActivityField
        name={props.name}
        type='number'
        label={t('Prize pool')}
        options={options}
        disabled={pools.isPending}
        onChange={(v) => {
          form.setValue(props.name, Number(v))
          form.setValue(
            props.name.replace(
              /pool_id$/,
              'pool_name'
            ) as FieldPath<MarketingActivity>,
            options.find((o) => o.value === v)?.label ?? ''
          )
        }}
      />
      <Input
        aria-label={t('Search prize pools')}
        placeholder={t('Search prize pools')}
        value={keyword}
        onChange={(e) => {
          setKeyword(e.target.value)
          setPage(1)
        }}
      />
      {pools.isError ? (
        <ErrorState onRetry={() => void pools.refetch()} />
      ) : (
        <>
          {pools.isPending && <LoadingState inline message={t('Loading...')} />}
          {!pools.isPending && pools.data?.items.length === 0 && (
            <p role='status' className='text-muted-foreground text-sm'>
              {t('No prize pools found')}
            </p>
          )}
          {(page > 1 || (pools.data?.total ?? 0) > 20) && (
            <div className='flex gap-2'>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                disabled={page === 1 || pools.isFetching}
                onClick={() => setPage(page - 1)}
              >
                {t('Previous')}
              </Button>
              <Button
                type='button'
                variant='ghost'
                size='sm'
                disabled={
                  !pools.data ||
                  page * 20 >= pools.data.total ||
                  pools.isFetching
                }
                onClick={() => setPage(page + 1)}
              >
                {t('Next')}
              </Button>
            </div>
          )}
        </>
      )}
    </div>
  )
}
