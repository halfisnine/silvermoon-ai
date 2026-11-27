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
import { render, screen } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'

import { usePricingPreferencesStore } from '@/stores/pricing-preferences-store'

import { PricingSummary } from '../pricing-summary'

afterEach(() => localStorage.clear())

it('shows a provider-specific decimal price even when ordinary model pricing is unset', () => {
  usePricingPreferencesStore.setState({ currency: 'USD' })
  const expression = 'tier("base", u("seconds") * 0.125)'
  const values = {
    'billing_setting.plugin_billing_expr': { supplier: expression },
  }
  render(
    <PricingSummary
      entry={{
        model_name: 'video',
        version: 'v1',
        configured: values,
        effective: values,
        plugin_variants: [
          {
            plugin_key: 'supplier',
            plugin_name: 'Saved video provider',
            configured: expression,
            effective: expression,
            compatible: true,
            usage_schema: {
              seconds: {
                type: 'number',
                unit: 'second',
                description: 'Video generation unit price',
              },
            },
          },
        ],
      }}
    />
  )
  expect(screen.getByText('Saved video provider')).toBeVisible()
  expect(screen.getByRole('cell', { name: '$0.125/s' })).toBeVisible()
})
