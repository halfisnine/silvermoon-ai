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
import { z } from 'zod'

import type { MarketingActivity, MarketingReward } from '../types'

const integer = z
  .number({ error: 'Enter a whole number' })
  .int('Enter a whole number')
  .min(0, 'Enter zero or a positive number')
  .max(Number.MAX_SAFE_INTEGER, 'The number is too large')
const positiveDecimal = (value: string | undefined) =>
  !!value && /^\d+(\.\d{1,12})?$/.test(value) && Number(value) > 0
const cny = (value: string) =>
  /^\d+(\.\d{1,2})?$/.test(value) && Number(value) > 0
const rewardSchema = z.object({
  kind: z.enum(['gift', 'chance']),
  calculation: z.enum(['fixed', 'paid_ratio', 'cash_ratio']).optional(),
  amount: z.string().optional(),
  currency: z.string().optional(),
  ratio_percent: z.string().optional(),
  valid_seconds: integer.optional(),
  pool_id: integer.optional(),
  pool_name: z.string().optional(),
  count: integer.optional(),
  chance_mode: z.enum(['direct', 'probability']).optional(),
  probability_percent: z.string().optional(),
})
export const activitySchema = z
  .object({
    name: z
      .string()
      .trim()
      .min(1, 'Name is required')
      .refine(
        (v) => new TextEncoder().encode(v).length <= 160,
        'Name is too long'
      ),
    kind: z.enum(['registration', 'checkin', 'recharge', 'invitation']),
    condition: z.string(),
    frequency: z.enum(['once', 'every']),
    published: z.boolean(),
    starts_at: integer.min(1, 'Select a start date'),
    ends_at: integer,
    threshold_cny: z.string(),
    exclusive_group: z
      .string()
      .trim()
      .refine(
        (v) => new TextEncoder().encode(v).length <= 64,
        'Name is too long'
      ),
    priority: z
      .number({ error: 'Enter a valid number' })
      .int('Enter a whole number')
      .min(-Number.MAX_SAFE_INTEGER, 'The number is too large')
      .max(Number.MAX_SAFE_INTEGER, 'The number is too large'),
    rewards: z.array(rewardSchema).max(20, 'Too many rewards'),
    checkin: z
      .object({
        condition: z.enum(['none', 'today_paid', 'previous_day_paid']),
        threshold_cny: z.string(),
        cycle_days: integer,
        makeup_days: integer,
        makeup_limit: integer,
        makeup_daily: z.boolean(),
        milestones: z
          .array(
            z.object({
              days: integer.min(1, 'Streak milestone must be positive'),
              rewards: z.array(rewardSchema).min(1, 'Add at least one reward').max(20, 'Too many rewards'),
            })
          )
          .max(100, 'Too many streak milestones'),
      })
      .nullable(),
  })
  .superRefine((activity, ctx) => {
    const fail = (path: (string | number)[], message: string) =>
      ctx.addIssue({ code: 'custom', path, message })
    const conditions: Record<string, string[]> = {
      registration: ['registration'],
      checkin: ['checkin'],
      recharge: ['first_payment', 'single_payment', 'cumulative_payments'],
      invitation: ['friend_registration', 'friend_cumulative', 'friend_ratio'],
    }
    if (!conditions[activity.kind].includes(activity.condition))
      {fail(['condition'], 'Invalid reward calculation')}
    if (
      ['registration', 'first_payment', 'friend_registration'].includes(
        activity.condition
      ) &&
      activity.frequency !== 'once'
    )
      {fail(['frequency'], 'Invalid reward calculation')}
    if (
      ['checkin', 'friend_ratio'].includes(activity.condition) &&
      activity.frequency !== 'every'
    )
      {fail(['frequency'], 'Invalid reward calculation')}
    if (activity.kind !== 'checkin' && activity.checkin)
      {fail(['checkin'], 'Invalid check-in settings')}
    if (activity.ends_at && activity.ends_at <= Date.now() / 1000)
      {fail(['ends_at'], 'Choose an end time in the future')}
    if (activity.ends_at && activity.ends_at <= activity.starts_at)
      {fail(['ends_at'], 'End date must be after start date')}
    if (
      (activity.kind === 'recharge' ||
        activity.condition === 'friend_cumulative') &&
      !cny(activity.threshold_cny)
    )
      {fail(
        ['threshold_cny'],
        'Enter a positive CNY amount with at most two decimal places'
      )}
    if (!activity.rewards.length && !activity.checkin?.milestones.length)
      {fail(['rewards'], 'Add at least one reward')}
    const groups = [
      { path: ['rewards'] as (string | number)[], rewards: activity.rewards },
      ...(activity.checkin?.milestones.map((m, i) => ({
        path: ['checkin', 'milestones', i, 'rewards'],
        rewards: m.rewards,
      })) ?? []),
    ]
    for (const group of groups) {
      for (const [index, reward] of group.rewards.entries()) {
        const path = [...group.path, index]
        if (reward.kind === 'gift') {
          if (reward.calculation === 'fixed' && !positiveDecimal(reward.amount))
            {fail([...path, 'amount'], 'Enter a positive amount')}
          if (
            reward.calculation !== 'fixed' &&
            !positiveDecimal(reward.ratio_percent)
          )
            {fail([...path, 'ratio_percent'], 'Enter a positive percentage')}
          if (
            reward.calculation === 'paid_ratio' &&
            (activity.kind !== 'recharge' ||
              activity.condition === 'cumulative_payments')
          )
            {fail(
              [...path, 'calculation'],
              'Cumulative rewards cannot use percentages'
            )}
          if (
            (reward.calculation === 'cash_ratio') !==
            (activity.condition === 'friend_ratio')
          )
            {fail([...path, 'calculation'], 'Invalid reward calculation')}
        } else {
          if (!reward.pool_id) fail([...path, 'pool_id'], 'Select a prize pool')
          if (!reward.count) fail([...path, 'count'], 'Enter a positive count')
          if (activity.condition === 'friend_ratio')
            {fail([...path, 'kind'], 'Invalid reward calculation')}
          if (reward.chance_mode === 'probability') {
            if (
              activity.kind !== 'recharge' ||
              activity.condition !== 'cumulative_payments'
            )
              {fail(
                [...path, 'chance_mode'],
                'Probability opportunities require cumulative recharge'
              )}
            const value = reward.probability_percent
            if (
              !value ||
              !/^\d+(\.\d{1,4})?$/.test(value) ||
              Number(value) > 100
            )
              {fail(
                [...path, 'probability_percent'],
                'Enter a percentage from 0 to 100 with at most four decimal places'
              )}
          }
        }
      }
    }
    const c = activity.checkin
    if (activity.kind === 'checkin' && !c)
      {fail(['checkin'], 'Invalid check-in settings')}
    if (c) {
      if (c.condition !== 'none' && !cny(c.threshold_cny))
        {fail(
          ['checkin', 'threshold_cny'],
          'Enter a positive CNY amount with at most two decimal places'
        )}
      if ((c.makeup_days === 0) !== (c.makeup_limit === 0))
        {fail(
          ['checkin', 'makeup_limit'],
          'Set both the make-up window and the activity allowance'
        )}
      const seen = new Set<number>()
      c.milestones.forEach((m, i) => {
        if (seen.has(m.days) || (c.cycle_days > 0 && m.days > c.cycle_days))
          {fail(
            ['checkin', 'milestones', i, 'days'],
            'Streak milestones must be unique and within the cycle'
          )}
        seen.add(m.days)
      })
    }
  })
export function defaultReward(currency: string): MarketingReward {
  return {
    kind: 'gift',
    calculation: 'fixed',
    amount: '',
    currency,
    valid_seconds: 0,
  }
}
export function defaultActivity(currency: string): MarketingActivity {
  return {
    name: '',
    kind: 'registration',
    condition: 'registration',
    frequency: 'once',
    published: true,
    starts_at: 0,
    ends_at: 0,
    threshold_cny: '',
    exclusive_group: '',
    priority: 0,
    rewards: [defaultReward(currency)],
    checkin: null,
  }
}
/** Reset only incompatible configuration when an operator changes the trigger. */
export function changeActivityCondition(
  activity: MarketingActivity,
  kind: MarketingActivity['kind'],
  condition: string,
  currency: string
): MarketingActivity {
  const next = {
    ...activity,
    kind,
    condition,
    threshold_cny: '',
    frequency: 'once' as 'once' | 'every',
    checkin: null as MarketingActivity['checkin'],
  }
  if (kind === 'checkin') {
    next.frequency = 'every'
    next.checkin = activity.checkin ?? {
      condition: 'none',
      threshold_cny: '',
      cycle_days: 0,
      makeup_days: 0,
      makeup_limit: 0,
      makeup_daily: false,
      milestones: [],
    }
  }
  if (condition === 'friend_ratio') {
    next.frequency = 'every'
    next.rewards = [
      {
        kind: 'gift',
        calculation: 'cash_ratio',
        ratio_percent: '',
        currency,
        valid_seconds: 0,
      },
    ]
  } else {
    next.rewards = activity.rewards.map((r) => {
      if (
        r.kind === 'gift' &&
        r.calculation !== 'fixed' &&
        (kind !== 'recharge' ||
          condition === 'cumulative_payments' ||
          r.calculation === 'cash_ratio')
      )
        {return defaultReward(currency)}
      if (
        r.kind === 'chance' &&
        (kind !== 'recharge' || condition !== 'cumulative_payments')
      )
        {return {
          kind: r.kind,
          pool_id: r.pool_id,
          pool_name: r.pool_name,
          count: r.count,
          chance_mode: 'direct' as const,
        }}
      return r
    })
  }
  return next
}
