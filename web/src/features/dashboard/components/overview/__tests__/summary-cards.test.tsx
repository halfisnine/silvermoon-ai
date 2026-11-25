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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import { SummaryCards } from '../summary-cards'

let client: QueryClient
let balance = {
  paid: { amount: '6', currency: 'CNY' },
  gift: { amount: '22.15', currency: 'CNY' },
}
let failed = false
beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  client.setQueryData(['status'], { display_in_currency: true })
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'alice', role: 1, quota: 300000 })
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      quotaDisplayType: 'CNY',
      quotaPerUnit: 50000,
      usdExchangeRate: 2,
    },
  })
  balance = {
    paid: { amount: '6', currency: 'CNY' },
    gift: { amount: '22.15', currency: 'CNY' },
  }
  failed = false
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/marketing/user/wallet') {
      if (failed) throw new Error('Network')
      return { data: { success: true, data: balance } }
    }
    if (url === '/api/data/self') {
      return { data: { success: true, data: [{ quota: 50000, count: 1 }] } }
    }
    throw new Error(`Unexpected request: ${url}`)
  })
})
afterEach(async () => {
  await act(() => i18next.changeLanguage('en'))
  client.clear()
  vi.restoreAllMocks()
  useAuthStore.setState(useAuthStore.getInitialState(), true)
  useSystemConfigStore.setState(useSystemConfigStore.getInitialState(), true)
  localStorage.clear()
})
async function view() {
  const router = createRouter({
    routeTree: createRootRoute({ component: SummaryCards }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  return render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}
test('overview shows the wallet paid and gift amounts without converting them again and links to gift details', async () => {
  await view()
  expect(await screen.findByText('¥22.15')).toBeVisible()
  expect(screen.getByText('¥6')).toBeVisible()
  expect(screen.getByText('Remaining paid amount')).toBeVisible()
  expect(screen.getByText('Remaining gift amount')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Gift details' })).toHaveAttribute(
    'href',
    '/activities/rewards?tab=gifts&source='
  )
  expect(screen.getByRole('button', { name: 'Wallet' })).toHaveAttribute(
    'href',
    '/wallet'
  )
  // One day used ¥2; ¥28.15 of available funds lasts about 14 days.
  expect(await screen.findByText('~14 days')).toBeVisible()
  i18next.addResourceBundle('zh', 'translation', zh.translation, true, true)
  await act(() => i18next.changeLanguage('zh'))
  expect(screen.getByText('剩余付费金额')).toBeVisible()
  expect(screen.getByText('剩余赠金')).toBeVisible()
})
test('gift-only funds remain available and tiny gifts are not rounded to zero', async () => {
  balance = {
    paid: { amount: '0', currency: 'CNY' },
    gift: { amount: '0.000001', currency: 'CNY' },
  }
  await view()
  expect(await screen.findByText('¥0.000001')).toBeVisible()
  expect(screen.queryByText('Balance depleted')).not.toBeInTheDocument()
  expect(await screen.findByText('Less than 1 day left')).toBeVisible()
})
test('failed wallet lookup shows retry without a false zero or depleted status and recovers', async () => {
  failed = true
  await view()
  const retry = await screen.findByRole('button', { name: 'Retry' })
  expect(screen.queryByText('Balance depleted')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('group', { name: 'Wallet' })
  ).not.toBeInTheDocument()
  failed = false
  await userEvent.click(retry)
  expect(await screen.findByText('¥22.15')).toBeVisible()
})
test('loading wallet does not claim a zero balance and a zero wallet is depleted once loaded', async () => {
  balance = {
    paid: { amount: '0', currency: 'CNY' },
    gift: { amount: '0', currency: 'CNY' },
  }
  let resolveBalance: (value: unknown) => void = () => undefined
  const response = new Promise((resolve) => {
    resolveBalance = resolve
  })
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url === '/api/marketing/user/wallet') {
      return (await response) as Awaited<ReturnType<typeof api.get>>
    }
    return { data: { success: true, data: [] } }
  })
  await view()
  expect(
    within(screen.getByRole('group', { name: 'Wallet' })).queryByText('¥0')
  ).not.toBeInTheDocument()
  expect(screen.queryByText('Balance depleted')).not.toBeInTheDocument()
  resolveBalance({ data: { success: true, data: balance } })
  await waitFor(() =>
    expect(screen.getAllByText('Balance depleted')).toHaveLength(2)
  )
})
