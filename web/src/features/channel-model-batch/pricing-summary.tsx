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

import type { ModelPricingEntry } from '@/features/model-pricing/api'
import { modelPricingDisplay } from '@/features/model-pricing/pricing'
import { DynamicPricingBreakdown } from '@/features/pricing/components/dynamic-pricing-breakdown'
import { ModelPriceCell } from '@/features/pricing/components/model-price-cell'

export function PricingSummary(props: { entry: ModelPricingEntry }) {
  const { t } = useTranslation()
  const values = props.entry.effective
  const hasPrice =
    values.ModelPrice !== undefined ||
    values.ModelRatio !== undefined ||
    Boolean(values['billing_setting.billing_expr'])
  const model = modelPricingDisplay(props.entry)
  return (
    <div className='min-w-0 space-y-2'>
      {hasPrice ? (
        <ModelPriceCell
          model={model}
          options={{ tokenUnit: 'M' }}
          showExpression={false}
        />
      ) : (
        <span className='text-muted-foreground'>{t('Unset price')}</span>
      )}
      {model.billing_mode === 'tiered_expr' && (
        <DynamicPricingBreakdown
          compact
          billingExpr={model.billing_expr}
          usageSchema={model.billing_usage_schema}
        />
      )}
      {Object.entries(values['billing_setting.plugin_billing_expr'] ?? {}).map(
        ([key, expression]) => {
          const variant = props.entry.plugin_variants?.find(
            (item) => item.plugin_key === key
          )
          return (
            <section key={key} className='space-y-1 border-t pt-2'>
              <p className='text-xs font-medium break-all'>
                {variant?.plugin_name ?? key}
              </p>
              <DynamicPricingBreakdown
                compact
                billingExpr={expression}
                usageSchema={variant?.usage_schema}
              />
            </section>
          )
        }
      )}
    </div>
  )
}
