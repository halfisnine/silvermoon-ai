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
import userEvent from '@testing-library/user-event'
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

test('activity filter includes all pages, selects a later activity and keeps record pagination separate', async () => {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
    const page =
      (config?.params as Record<string, number> | undefined)?.page ?? 1
    if (url === '/api/marketing/admin/activities') {
      return {
        data: {
          success: true,
          data: {
            items:
              page === 1
                ? Array.from({ length: 100 }, (_, i) => ({
                    id: i + 1,
                    activity: { name: `Activity ${i + 1}` },
                  }))
                : [{ id: 101, activity: { name: 'Final activity' } }],
            total: 101,
            page,
            page_size: 100,
          },
        },
      }
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
  const user = userEvent.setup()
  const activity = await screen.findByRole('combobox', { name: 'Activity' })
  await waitFor(() => expect(activity).toBeEnabled())
  await user.click(activity)
  await user.type(activity, 'Final')
  expect(
    await screen.findByRole('option', { name: 'Final activity' })
  ).toBeVisible()
  expect(
    screen.queryByRole('option', { name: 'Activity 1' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Previous' })
  ).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Next' })).not.toBeInTheDocument()
  await user.click(screen.getByRole('option', { name: 'Final activity' }))
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith('/api/marketing/admin/rewards', {
      params: expect.objectContaining({ activity_id: 101, page: 1 }),
    })
  )
})

test('reward filters have visible labels and a single searchable activity selector', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { items: [], total: 0, page: 1, page_size: 20 },
    },
  })
  render(
    <QueryClientProvider client={client}>
      <MarketingRewardsPage />
    </QueryClientProvider>
  )
  await screen.findByText('No reward records found')
  for (const label of [
    'Reward source',
    'Username',
    'Activity',
    'From',
    'Until (exclusive)',
  ]) {
    expect(screen.getByText(label, { selector: 'label' })).toBeVisible()
    expect(
      screen.getByLabelText(label, {
        selector: label === 'Activity' ? 'input' : '*',
      })
    ).toBeVisible()
  }
})

test('switching reward source clears activity filtering and preserves the username filter', async () => {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: {
        items:
          url === '/api/marketing/admin/activities'
            ? [{ id: 7, activity: { name: 'Welcome rewards' } }]
            : [],
        total: url === '/api/marketing/admin/activities' ? 1 : 0,
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
  const user = userEvent.setup()
  const activity = await screen.findByRole('combobox', { name: 'Activity' })
  await waitFor(() => expect(activity).toBeEnabled())
  await user.click(activity)
  fireEvent.change(screen.getByLabelText('Username'), {
    target: { value: 'alice' },
  })
  await user.click(
    await screen.findByRole('option', { name: 'Welcome rewards' })
  )
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith('/api/marketing/admin/rewards', {
      params: expect.objectContaining({
        kind: 'activity',
        username: 'alice',
        activity_id: 7,
      }),
    })
  )
  fireEvent.change(screen.getByLabelText('Reward source'), {
    target: { value: 'lottery' },
  })
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith('/api/marketing/admin/rewards', {
      params: expect.objectContaining({
        kind: 'lottery',
        username: 'alice',
        activity_id: 0,
        page: 1,
      }),
    })
  )
  expect(screen.queryByLabelText('Activity')).not.toBeInTheDocument()
  expect(screen.queryByLabelText('Search activities')).not.toBeInTheDocument()
  expect(screen.getByLabelText('From')).toBeVisible()
  expect(screen.getByLabelText('Until (exclusive)')).toBeVisible()
  fireEvent.change(screen.getByLabelText('Reward source'), {
    target: { value: 'activity' },
  })
  expect(await screen.findByRole('combobox', { name: 'Activity' })).toHaveValue(
    'All activities'
  )
  expect(screen.getByLabelText('Username')).toHaveValue('alice')
})

test('activity search shows no matches, restores choices and clears the selected record filter', async () => {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: {
        items:
          url === '/api/marketing/admin/activities'
            ? [{ id: 7, activity: { name: 'Welcome rewards' } }]
            : [],
        total: url === '/api/marketing/admin/activities' ? 1 : 0,
        page: 1,
        page_size: 100,
      },
    },
  }))
  render(
    <QueryClientProvider client={client}>
      <MarketingRewardsPage />
    </QueryClientProvider>
  )
  const user = userEvent.setup()
  const activity = await screen.findByRole('combobox', { name: 'Activity' })
  await waitFor(() => expect(activity).toBeEnabled())
  await user.click(activity)
  await user.type(activity, 'Missing activity')
  expect(await screen.findByText('No results found')).toBeVisible()
  await user.clear(activity)
  await user.click(
    await screen.findByRole('option', { name: 'Welcome rewards' })
  )
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/marketing/admin/rewards', {
      params: expect.objectContaining({ activity_id: 7 }),
    })
  )
  expect(activity).toHaveValue('Welcome rewards')
  await user.click(activity)
  await user.click(
    await screen.findByRole('option', { name: 'All activities' })
  )
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/marketing/admin/rewards', {
      params: expect.objectContaining({ activity_id: 0, page: 1 }),
    })
  )
  expect(activity).toHaveValue('All activities')
})

test('failed activity choices offer a retry instead of silently displaying incomplete options', async () => {
  let unavailable = true
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/marketing/admin/activities' && unavailable) {
      throw new Error('network unavailable')
    }
    return {
      data: {
        success: true,
        data: {
          items:
            url === '/api/marketing/admin/activities'
              ? [{ id: 7, activity: { name: 'Welcome rewards' } }]
              : [],
          total: url === '/api/marketing/admin/activities' ? 1 : 0,
          page: 1,
          page_size: 100,
        },
      },
    }
  })
  render(
    <QueryClientProvider client={client}>
      <MarketingRewardsPage />
    </QueryClientProvider>
  )
  const user = userEvent.setup()
  await screen.findByRole('button', { name: 'Retry' })
  expect(
    screen.queryByRole('combobox', { name: 'Activity' })
  ).not.toBeInTheDocument()
  unavailable = false
  await user.click(screen.getByRole('button', { name: 'Retry' }))
  const activity = await screen.findByRole('combobox', { name: 'Activity' })
  await waitFor(() => expect(activity).toBeEnabled())
  await user.click(activity)
  expect(
    await screen.findByRole('option', { name: 'Welcome rewards' })
  ).toBeVisible()
})
