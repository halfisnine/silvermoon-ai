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

test.each([false, true])(
  'negative paid balance shows exact debt and automatic recharge offset in compact=%s',
  async (compact) => {
    await view(
      <WalletStatsCard
        compact={compact}
        balance={{
          paid: { amount: '-12.000001', currency: 'CNY' },
          gift: { amount: '2', currency: 'CNY' },
        }}
      />
    )
    expect(screen.getByText('¥-12.000001')).toHaveClass('text-destructive')
    expect(screen.getByText('¥2')).not.toHaveClass('text-destructive')
    expect(screen.getByRole('status')).toHaveTextContent(
      'Amount owed: ¥12.000001. Your next recharge will automatically offset this debt.'
    )
    expect(screen.getByRole('status')).toHaveClass('col-span-2', 'break-words')
    expect(screen.getAllByRole('status')).toHaveLength(1)
    await act(() => i18next.changeLanguage('zh'))
    expect(screen.getByRole('status')).toHaveTextContent(
      '当前欠款：¥12.000001。下次充值将自动抵扣欠款。'
    )
    expect(screen.getByText('¥-12.000001')).toHaveClass('text-destructive')
  }
)

test.each(['100', '0', '-0.000000'])(
  'paid balance %s shows no debt warning',
  async (paid) => {
    await view(
      <WalletStatsCard
        balance={{
          paid: { amount: paid, currency: 'CNY' },
          gift: { amount: '0', currency: 'CNY' },
        }}
      />
    )
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    expect(document.querySelector('.text-destructive')).not.toBeInTheDocument()
  }
)

test('a tiny negative paid amount retains its debt precision without rounding to zero', async () => {
  await view(
    <WalletStatsCard
      balance={{
        paid: { amount: '-0.000000000000000001', currency: 'USD' },
        gift: { amount: '0', currency: 'USD' },
      }}
    />
  )
  expect(screen.getByText('$-0.000000000000000001')).toHaveClass(
    'text-destructive'
  )
  expect(screen.getByRole('status')).toHaveTextContent(
    'Amount owed: $0.000000000000000001.'
  )
})

test.each([{ loading: true }, { error: true }])(
  'unavailable wallet data %j suppresses a stale debt warning',
  async (state) => {
    await view(
      <WalletStatsCard
        {...state}
        balance={{
          paid: { amount: '-12', currency: 'CNY' },
          gift: { amount: '0', currency: 'CNY' },
        }}
      />
    )
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    expect(screen.queryByText('¥-12')).not.toBeInTheDocument()
  }
)

test('an invalid paid amount stays unavailable without presenting a debt amount', async () => {
  await view(
    <WalletStatsCard
      balance={{
        paid: { amount: '-unknown', currency: 'CNY' },
        gift: { amount: '0', currency: 'CNY' },
      }}
    />
  )
  expect(screen.getByText('—')).toBeVisible()
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
})
