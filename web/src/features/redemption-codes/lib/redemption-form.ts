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
import type { TFunction } from 'i18next'
import { z } from 'zod'

import type { RedemptionFormData, Redemption } from '../types'

export function getRedemptionFormSchema(t: TFunction) {
  return z.object({
    name: z
      .string()
      .trim()
      .refine(
        (value) => [...value].length >= 1 && [...value].length <= 20,
        t('Redemption code name length must be between 1-20')
      ),
    paid_amount_cny: z
      .string()
      .trim()
      .regex(
        /^(0|[1-9][0-9]{0,13})(\.[0-9]{1,2})?$/,
        t(
          'Enter a positive payment amount in CNY with up to two decimal places'
        )
      )
      .refine(
        (value) => Number(value) > 0,
        t(
          'Enter a positive payment amount in CNY with up to two decimal places'
        )
      ),
    credit_amount: z
      .string()
      .trim()
      .regex(
        /^[0-9]{1,16}(\.[0-9]{1,12})?$/,
        t('Enter a positive credit amount with up to twelve decimal places')
      )
      .refine(
        (value) => Number(value) > 0,
        t('Enter a positive credit amount with up to twelve decimal places')
      ),
    sales_channel: z
      .string()
      .trim()
      .refine(
        (value) => [...value].length >= 1 && [...value].length <= 80,
        t('Enter a sales channel of 1 to 80 characters')
      ),
    sales_batch: z
      .string()
      .trim()
      .refine(
        (value) => [...value].length >= 1 && [...value].length <= 80,
        t('Enter a sales batch of 1 to 80 characters')
      ),
    sales_evidence: z
      .string()
      .trim()
      .refine(
        (value) => [...value].length <= 1000,
        t('Sales evidence must not exceed 1000 characters')
      ),
    expired_time: z
      .date()
      .nullable()
      .refine(
        (date) => date === null || date.getTime() > Date.now(),
        t('Expiration time cannot be earlier than current time')
      ),
    count: z
      .number()
      .int(t('Select between 1 and 100 paid redemption codes'))
      .min(1, t('Select between 1 and 100 paid redemption codes'))
      .max(100, t('Select between 1 and 100 paid redemption codes')),
  })
}

export type RedemptionFormValues = z.infer<
  ReturnType<typeof getRedemptionFormSchema>
>
export const REDEMPTION_FORM_DEFAULT_VALUES: RedemptionFormValues = {
  name: '',
  paid_amount_cny: '',
  credit_amount: '',
  sales_channel: '',
  sales_batch: '',
  sales_evidence: '',
  expired_time: null,
  count: 1,
}

export function transformFormDataToPayload(
  data: RedemptionFormValues,
  currency: string,
  moneyRevision: string
): RedemptionFormData {
  return {
    ...data,
    credit_currency: currency,
    money_revision: moneyRevision,
    expired_time: data.expired_time
      ? Math.floor(data.expired_time.getTime() / 1000)
      : 0,
  }
}

export function transformRedemptionToFormDefaults(
  redemption: Redemption
): RedemptionFormValues {
  return {
    name: redemption.name,
    paid_amount_cny: redemption.paid_amount_cny,
    credit_amount: redemption.credit_amount,
    sales_channel: redemption.sales_channel,
    sales_batch: redemption.sales_batch,
    sales_evidence: redemption.sales_evidence,
    expired_time:
      redemption.expired_time > 0
        ? new Date(redemption.expired_time * 1000)
        : null,
    count: 1,
  }
}
