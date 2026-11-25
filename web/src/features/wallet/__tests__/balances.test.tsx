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
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { beforeEach, afterEach, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'

import { WalletStatsCard } from '../components/wallet-stats-card'

beforeEach(async () => {
  i18next.addResourceBundle('en', 'translation', en.translation, true, true)
  i18next.addResourceBundle('zh', 'translation', zh.translation, true, true)
  await i18next.changeLanguage('en')
})
afterEach(async () => {
  await act(() => i18next.changeLanguage('en'))
  vi.restoreAllMocks()
})
async function view(node: React.ReactNode) {
  const router = createRouter({
    routeTree: createRootRoute({ component: () => node }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  render(<RouterProvider router={router} />)
}
test('wallet shows only paid and gift totals and links to the existing gift tab', async () => {
  await view(
    <WalletStatsCard
      balance={{
        paid: { amount: '100', currency: 'CNY' },
        gift: { amount: '0.000001', currency: 'CNY' },
      }}
    />
  )
  expect(screen.getByText('Remaining paid amount')).toBeVisible()
  expect(screen.getByText('Remaining gift amount')).toBeVisible()
  expect(screen.getByText('¥0.000001')).toBeVisible()
  expect(screen.queryByText('Total Usage')).not.toBeInTheDocument()
  expect(screen.queryByText('API Requests')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Gift details' })).toHaveAttribute(
    'href',
    '/activities/rewards?tab=gifts&source='
  )
  await act(() => i18next.changeLanguage('zh'))
  expect(screen.getByText('剩余付费金额')).toBeVisible()
  expect(screen.getByText('剩余赠金')).toBeVisible()
})
test('wallet fetch failure is visible and retry does not present a false zero balance', async () => {
  const retry = vi.fn()
  await view(<WalletStatsCard error onRetry={retry} />)
  expect(screen.queryByText('¥0')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(retry).toHaveBeenCalledOnce()
})
