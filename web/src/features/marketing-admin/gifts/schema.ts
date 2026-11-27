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

export const giftIssueSchema = z
  .object({
    user_ids: z
      .array(z.number().int().positive())
      .min(1, 'Select at least one user')
      .max(100, 'Select up to 100 users'),
    amount: z
      .string()
      .regex(
        /^(?:0|[1-9]\d{0,15})(?:\.\d{1,12})?$/,
        'Enter a positive amount with up to 12 decimal places'
      )
      .refine((v) => Number(v) > 0, 'Enter a positive amount'),
    permanent: z.boolean(),
    duration: z.number({ error: 'Enter a valid number' }),
    unit: z.enum(['days', 'hours']),
    reason: z
      .string()
      .trim()
      .min(1, 'Enter an internal issuance reason')
      .max(500, 'The reason is too long'),
  })
  .superRefine((v, ctx) => {
    const seconds = v.duration * (v.unit === 'days' ? 86400 : 3600)
    if (
      !v.permanent &&
      (!Number.isSafeInteger(seconds) || seconds < 1 || seconds > 3153600000)
    ) {
      ctx.addIssue({
        code: 'custom',
        path: ['duration'],
        message: 'Enter a validity period between 1 second and 36500 days',
      })
    }
  })
export type GiftIssueValues = z.infer<typeof giftIssueSchema>

// Multiplication by an integer preserves every entered decimal place; binary
// floating-point rounding must not change the total shown before issuing.
export function giftIssueTotal(amount: string, count: number): string {
  if (!/^(?:0|[1-9]\d*)(?:\.\d{1,12})?$/.test(amount)) return '0'
  const [whole, fraction = ''] = amount.split('.')
  const digits = (BigInt(whole + fraction) * BigInt(count))
    .toString()
    .padStart(fraction.length + 1, '0')
  if (!fraction.length) return digits
  return `${digits.slice(0, -fraction.length)}.${digits.slice(-fraction.length)}`
}
