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

import { MarketingGiftsPage } from '../index'

const clients: QueryClient[] = []
afterEach(() => {
  clients.forEach((c) => c.clear())
  clients.length = 0
  vi.restoreAllMocks()
})
function setup(fail = false) {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/marketing/admin/metadata') {
      return {
        data: {
          success: true,
          data: { currency: 'CNY', money_revision: 'rev1' },
        },
      }
    }
    if (fail) throw new Error('network failed')
    return {
      data: {
        success: true,
        data: { items: [], total: 0, page: 1, page_size: 20 },
      },
    }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <MarketingGiftsPage />
    </QueryClientProvider>
  )
  return get
}

test('the management menu page separates all gifts from manual batches and sends gift source, status and validity filters', async () => {
  const get = setup()
  const user = userEvent.setup()
  expect(await screen.findByText('No gift credits found')).toBeVisible()
  expect(screen.getByRole('tab', { name: 'All gift credits' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  fireEvent.change(screen.getByLabelText('Source'), {
    target: { value: 'admin_gift' },
  })
  fireEvent.change(screen.getByLabelText('Status'), {
    target: { value: 'expired' },
  })
  fireEvent.change(screen.getByLabelText('Validity'), {
    target: { value: 'limited' },
  })
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith(
      '/api/marketing/admin/gifts',
      expect.objectContaining({
        params: expect.objectContaining({
          kind: 'admin_gift',
          state: 'expired',
          validity: 'limited',
        }),
      })
    )
  )
  await user.click(screen.getByRole('tab', { name: 'Manual issuance records' }))
  expect(await screen.findByText('No issuance records found')).toBeVisible()
  expect(
    screen.getByRole('tab', { name: 'Manual issuance records' })
  ).toHaveAttribute('aria-selected', 'true')
  expect(screen.queryByLabelText('Source')).not.toBeInTheDocument()
  expect(get).toHaveBeenCalledWith(
    '/api/marketing/admin/gift-batches',
    expect.anything()
  )
})

test('a failed gift query offers a retry instead of pretending the data is empty', async () => {
  setup(true)
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Retry' })).toBeVisible()
  )
  expect(screen.queryByText('No gift credits found')).not.toBeInTheDocument()
})
