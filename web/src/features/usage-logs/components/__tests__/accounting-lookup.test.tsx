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
import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { beforeEach, afterEach, expect, test, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { AccountingQueryDialog } from '../accounting-query-dialog'
import { UsageLogsProvider } from '../usage-logs-provider'

let client: QueryClient
beforeEach(async () => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  i18next.addResourceBundle('en', 'translation', en.translation, true, true)
  await i18next.changeLanguage('en')
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'admin', role: ROLE.ADMIN })
})
afterEach(async () => {
  await act(() => i18next.changeLanguage('en'))
  client.clear()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})
async function view() {
  const router = createRouter({
    routeTree: createRootRoute({
      component: () => (
        <UsageLogsProvider>
          <AccountingQueryDialog />
        </UsageLogsProvider>
      ),
    }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}
test('ordinary users cannot open administrator accounting lookup or send its queries', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 2, username: 'alice', role: ROLE.USER })
  const get = vi.spyOn(api, 'get')
  await view()
  expect(
    screen.queryByRole('button', { name: 'Accounting lookup' })
  ).not.toBeInTheDocument()
  expect(get).not.toHaveBeenCalled()
})
test('blank owner is omitted, invalid owner prevents search, and verified payments expose safe source details', async () => {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: {
        items: String(url).endsWith('/payments')
          ? [
              {
                id: 8,
                user_id: 2,
                kind: 'redemption',
                paid_cny: '10',
                credited: { amount: '12', currency: 'CNY' },
                posted_at: 1795392000,
                redemption_id: 12,
                redemption_name: 'November paid codes',
                sales_channel: 'Shop',
                sales_batch: 'November',
                fact_id: 22,
              },
            ]
          : [],
        total: String(url).endsWith('/payments') ? 1 : 0,
        page: 1,
        page_size: 20,
      },
    },
  }))
  await view()
  await userEvent.click(
    screen.getByRole('button', { name: 'Accounting lookup' })
  )
  await screen.findByText('No accounting records found')
  expect(get.mock.calls[0][1]?.params).not.toHaveProperty('user_id')
  const owner = screen.getByRole('textbox', { name: 'User ID' })
  await userEvent.type(owner, '-2')
  expect(screen.getByRole('button', { name: 'Search' })).toBeDisabled()
  expect(owner).toHaveAttribute('aria-invalid', 'true')
  await userEvent.clear(owner)
  await userEvent.click(screen.getByRole('tab', { name: 'Verified payments' }))
  await screen.findByText('¥10')
  await userEvent.click(screen.getByRole('button', { name: 'Details' }))
  expect(screen.getByText('November paid codes')).toBeVisible()
  expect(screen.getByText('Shop')).toBeVisible()
  expect(screen.getByText('¥12')).toBeVisible()
  expect(screen.getByText('22')).toBeVisible()
})
test('request filter accepts a single detail response and refund tab is independently queried', async () => {
  const request = {
    id: 10,
    user_id: 2,
    request_id: 'req-safe',
    model_name: 'model-safe',
    qualified_at: 1,
    state: 'review',
    conversion_known: false,
    cost_known: false,
    final_observed: false,
    total: null,
    paid: null,
    gift: null,
    unsettled: null,
    reserved: null,
    gift_sources: [],
  }
  const get = vi.spyOn(api, 'get').mockImplementation(async (_url, config) => ({
    data: {
      success: true,
      data: (config as { params?: { request_id?: string } } | undefined)?.params
        ?.request_id
        ? request
        : { items: [], total: 0, page: 1, page_size: 20 },
    },
  }))
  await view()
  await userEvent.click(
    screen.getByRole('button', { name: 'Accounting lookup' })
  )
  await userEvent.type(
    screen.getByRole('textbox', { name: 'Request ID' }),
    'req-safe'
  )
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  expect(await screen.findByText('model-safe')).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: 'Details' }))
  expect(
    screen.getByText(
      'The final usage result has not been confirmed. This is not a free request.'
    )
  ).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: 'Back to results' }))
  await userEvent.click(screen.getByRole('tab', { name: 'Cash refunds' }))
  await screen.findByText('No accounting records found')
  expect(
    get.mock.calls.some(([url]) => url === '/api/marketing/admin/cash-refunds')
  ).toBe(true)
  const dialog = screen.getByRole('dialog')
  expect(
    within(dialog).queryByRole('button', { name: 'Refund' })
  ).not.toBeInTheDocument()
})
