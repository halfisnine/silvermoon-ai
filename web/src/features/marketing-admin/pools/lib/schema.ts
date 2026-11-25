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

import type { PoolSpec } from '../types'

const nameSchema = z
  .string()
  .trim()
  .min(1, 'A name is required')
  .refine(
    (value) => new TextEncoder().encode(value).length <= 160,
    'The name is too long'
  )
const integer = z
  .number({ error: 'Enter a valid number' })
  .int('Enter a whole number')
  .min(0, 'Enter zero or a positive number')
  .max(Number.MAX_SAFE_INTEGER, 'The number is too large')
const giftSchema = z
  .object({
    amount: z
      .string()
      .regex(
        /^(?:0|[1-9]\d*)(?:\.\d{1,12})?$/,
        'Enter a positive amount with up to 12 decimal places'
      )
      .refine(
        (value) => Number(value) > 0 && Number.isFinite(Number(value)),
        'Enter a positive amount'
      ),
    permanent: z.boolean(),
    duration: z
      .number({ error: 'Enter a valid number' })
      .min(0, 'Enter zero or a positive number'),
    unit: z.enum(['days', 'hours', 'minutes']),
  })
  .superRefine((gift, ctx) => {
    const duration = gift.duration * durationUnits[gift.unit]
    if (
      !gift.permanent &&
      (!Number.isSafeInteger(Math.round(duration)) || duration < 1)
    ) {
      ctx.addIssue({
        code: 'custom',
        path: ['duration'],
        message: 'Enter a positive validity period',
      })
    }
  })
export const durationUnits = { days: 86400, hours: 3600, minutes: 60 } as const
export const poolFormSchema = z
  .object({
    name: nameSchema,
    style: z.enum(['wheel', 'blind_box']),
    starts_at: integer.min(1, 'Select a start time'),
    ends_at: integer.min(1, 'Select an end time'),
    capacity: integer.min(1, 'Enter a finite positive chance limit'),
    allow_empty: z.boolean(),
    prizes: z
      .array(
        z.object({
          name: nameSchema,
          weight: integer,
          stock: integer.or(z.literal(-1)),
          gifts: z.array(giftSchema).max(20, 'Too many gift items'),
        })
      )
      .min(1, 'Add at least one prize')
      .max(100, 'Too many prizes'),
  })
  .superRefine((pool, ctx) => {
    if (pool.ends_at <= pool.starts_at) {
      ctx.addIssue({
        code: 'custom',
        path: ['ends_at'],
        message: 'The end time must be later than the start time',
      })
    }
    if (pool.ends_at <= Math.floor(Date.now() / 1000)) {
      ctx.addIssue({
        code: 'custom',
        path: ['ends_at'],
        message: 'Choose an end time in the future',
      })
    }
    if (!pool.prizes.some((prize) => prize.weight > 0)) {
      ctx.addIssue({
        code: 'custom',
        path: ['prizes'],
        message: 'At least one prize must have a positive weight',
      })
    }
    pool.prizes.forEach((prize, index) => {
      if (prize.gifts.length === 0 && !pool.allow_empty) {
        ctx.addIssue({
          code: 'custom',
          path: ['prizes', index, 'gifts'],
          message: 'Enable no-prize outcomes before adding an empty prize',
        })
      }
    })
    const payable = pool.prizes.filter(
      (prize) => prize.gifts.length > 0 && prize.weight > 0
    )
    if (
      !payable.some((prize) => prize.stock === -1) &&
      payable.reduce((sum, prize) => sum + prize.stock, 0) < pool.capacity
    ) {
      ctx.addIssue({
        code: 'custom',
        path: ['capacity'],
        message:
          'Gift prize stock must cover the total chance limit; empty prizes do not add supply',
      })
    }
  })
export type PoolFormValues = z.infer<typeof poolFormSchema>
export const newGift =
  (): PoolFormValues['prizes'][number]['gifts'][number] => ({
    amount: '',
    permanent: true,
    duration: 0,
    unit: 'days',
  })
export const newPrize = (): PoolFormValues['prizes'][number] => ({
  name: '',
  weight: 1,
  stock: -1,
  gifts: [newGift()],
})
export function poolFormDefaults(
  pool?: PoolSpec,
  copy = false
): PoolFormValues {
  if (!pool) {
    return {
      name: '',
      style: 'wheel',
      starts_at: 0,
      ends_at: 0,
      capacity: 0,
      allow_empty: false,
      prizes: [newPrize()],
    }
  }
  return {
    ...pool,
    name: copy ? '' : pool.name,
    starts_at: copy ? 0 : pool.starts_at,
    ends_at: copy ? 0 : pool.ends_at,
    allow_empty: pool.prizes.some((prize) => prize.gifts.length === 0),
    prizes: pool.prizes.map((prize) => ({
      name: prize.name,
      weight: prize.weight,
      stock: prize.stock,
      gifts: prize.gifts.map((gift) => {
        const seconds = gift.valid_seconds
        let unit: keyof typeof durationUnits = 'minutes'
        if (seconds % 86400 === 0) unit = 'days'
        else if (seconds % 3600 === 0) unit = 'hours'
        return {
          amount: gift.amount,
          permanent: seconds === 0,
          duration: seconds / durationUnits[unit],
          unit,
        }
      }),
    })),
  }
}
export function poolFormPayload(
  values: PoolFormValues,
  currency: string
): PoolSpec {
  return {
    name: values.name,
    style: values.style,
    starts_at: values.starts_at,
    ends_at: values.ends_at,
    capacity: values.capacity,
    prizes: values.prizes.map((prize) => ({
      name: prize.name,
      weight: prize.weight,
      stock: prize.stock,
      gifts: prize.gifts.map((gift) => ({
        amount: gift.amount,
        currency,
        valid_seconds: gift.permanent
          ? 0
          : Math.round(gift.duration * durationUnits[gift.unit]),
      })),
    })),
  }
}
