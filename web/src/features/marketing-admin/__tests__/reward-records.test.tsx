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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { MarketingRewardsPage } from '../rewards'

const client = new QueryClient({
  defaultOptions: { queries: { retry: false } },
})
afterEach(() => {
  client.clear()
  vi.restoreAllMocks()
})
const record = {
  id: 87654,
  kind: 'activity',
  username: 'alice',
  source_name: 'Recharge celebration',
  state: 'credited',
  occurred_at: 1795132800,
  credited_at: 1795132810,
  rewards: [
    {
      kind: 'gift',
      calculation: 'fixed',
      amount: '0.000002',
      currency: 'CNY',
      valid_seconds: 7200,
      credited_at: 1795132810,
      expires_at: 1795140010,
    },
  ],
  source: {
    activity_id: 43219,
    trigger: 'payment',
    payment_kind: 'redemption',
    paid_cny: '10',
    sales_channel: 'Test shop',
    sales_batch: 'November sale',
    redemption_name: 'Paid shop code',
  },
}

test('reward details display real paid-code provenance and tiny gifts without exposing internal identifiers', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/marketing/admin/rewards/activity/87654'
          ? record
          : {
              items: url === '/api/marketing/admin/rewards' ? [record] : [],
              total: url === '/api/marketing/admin/rewards' ? 1 : 0,
              page: 1,
              page_size: 20,
            },
    },
  }))
  render(
    <QueryClientProvider client={client}>
      <MarketingRewardsPage />
    </QueryClientProvider>
  )
  await screen.findByText('Recharge celebration')
  expect(screen.getByText(/0\.000002/)).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: 'Details' }))
  expect(await screen.findByText('Paid shop code')).toBeVisible()
  expect(screen.getByText('November sale')).toBeVisible()
  expect(screen.getByText('Test shop')).toBeVisible()
  expect(screen.queryByText('87654')).not.toBeInTheDocument()
  expect(screen.queryByText('43219')).not.toBeInTheDocument()
})

test('failed record loading can be retried and username filtering uses the visible name', async () => {
  let failed = false
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/marketing/admin/rewards' && !failed) {
      failed = true
      throw new Error('network unavailable')
    }
    return {
      data: {
        success: true,
        data: { items: [], total: 0, page: 1, page_size: 20 },
      },
    }
  })
  render(
    <QueryClientProvider client={client}>
      <MarketingRewardsPage />
    </QueryClientProvider>
  )
  fireEvent.click(await screen.findByRole('button', { name: 'Retry' }))
  await screen.findByText('No reward records found')
  fireEvent.change(screen.getByLabelText('Username'), {
    target: { value: 'alice' },
  })
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith('/api/marketing/admin/rewards', {
      params: expect.objectContaining({ username: 'alice', page: 1 }),
    })
  )
})
