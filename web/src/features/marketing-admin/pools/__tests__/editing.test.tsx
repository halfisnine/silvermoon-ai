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

import { getAllActivityOptions, getPoolOptions } from '../../api'
import { OrganizationActionDialog } from '../../OrganizationActionDialog'
import { getPools } from '../api'
import { MarketingPoolsPage } from '../index'
import { PoolActionDialog } from '../PoolActionDialog'
import { PoolEditor } from '../PoolEditor'
import type { PoolRecord } from '../types'

const future = Math.floor(Date.now() / 1000) + 86400
const record: PoolRecord = {
  archived: false,
  deletable: true,
  id: 48,
  revision: 2,
  money_revision: 'original-money',
  state: 'scheduled',
  editable: true,
  issued: 0,
  remaining_capacity: 3,
  appendable_capacity: 2,
  pool: {
    name: 'Test pool',
    style: 'wheel',
    starts_at: future,
    ends_at: future + 86400,
    capacity: 3,
    prizes: [
      {
        id: 81,
        name: 'Gift prize',
        weight: 1,
        stock: 5,
        gifts: [{ amount: '2', currency: 'CNY', valid_seconds: 0 }],
      },
    ],
  },
}
const clients: QueryClient[] = []
function wrap(element: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  return render(element, {
    wrapper: ({ children }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  })
}
afterEach(() => {
  clients.forEach((client) => client.clear())
  clients.length = 0
})

test('invalid amount can be corrected and failed saves can be retried without duplicating the operation', async () => {
  const user = userEvent.setup()
  const put = vi
    .spyOn(api, 'put')
    .mockRejectedValueOnce(new Error('Network unavailable'))
    .mockRejectedValueOnce(new Error('Network unavailable'))
    .mockResolvedValueOnce({ data: { success: true, data: record } })
  const onClose = vi.fn()
  wrap(
    <PoolEditor
      record={record}
      currency='CNY'
      moneyRevision='original-money'
      onClose={onClose}
    />
  )
  const amount = screen.getByRole('textbox', { name: 'Gift amount (CNY)' })
  await user.clear(amount)
  await user.type(amount, '0')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  expect(await screen.findByText('Enter a positive amount')).toBeVisible()
  expect(put).not.toHaveBeenCalled()
  await user.clear(amount)
  await user.type(amount, '0.000001')
  await user.click(screen.getByRole('button', { name: 'Save' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Network unavailable'
  )
  expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled()
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(put).toHaveBeenCalledTimes(2))
  expect(
    (put.mock.calls[0][1] as { operation_key: string }).operation_key
  ).toBe((put.mock.calls[1][1] as { operation_key: string }).operation_key)
  await user.type(
    screen.getByRole('textbox', { name: 'Prize pool name' }),
    ' corrected'
  )
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(onClose).toHaveBeenCalled())
  expect(
    (put.mock.calls[2][1] as { operation_key: string }).operation_key
  ).not.toBe((put.mock.calls[0][1] as { operation_key: string }).operation_key)
  expect(put.mock.calls[2][1]).toMatchObject({
    money_revision: 'original-money',
    revision: 2,
    pool: {
      prizes: [
        { gifts: [{ amount: '0.000001', currency: 'CNY', valid_seconds: 0 }] },
      ],
    },
  })
})

test('empty outcomes are explicit and combination gifts remain separate editable entries', async () => {
  const user = userEvent.setup()
  wrap(
    <PoolEditor
      record={record}
      currency='CNY'
      moneyRevision='original-money'
      onClose={vi.fn()}
    />
  )
  expect(
    screen.queryByText(
      'A no-prize result consumes one chance and does not credit a gift.'
    )
  ).not.toBeInTheDocument()
  await user.click(
    screen.getByRole('checkbox', { name: 'Allow no-prize outcomes' })
  )
  expect(
    screen.getByText(
      'A no-prize result consumes one chance and does not credit a gift.'
    )
  ).toBeVisible()
  await user.click(
    screen.getByRole('checkbox', { name: 'Allow no-prize outcomes' })
  )
  expect(
    screen.queryByText(
      'A no-prize result consumes one chance and does not credit a gift.'
    )
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Add combination gift' }))
  expect(
    screen.getAllByRole('textbox', { name: 'Gift amount (CNY)' })
  ).toHaveLength(2)
  expect(
    screen.getByRole('button', { name: 'Remove gift 2 from prize 1' })
  ).toBeEnabled()
})

test('locked core rules disable edits and the dialog retains a bounded scrolling body', () => {
  wrap(
    <PoolEditor
      record={{ ...record, editable: false }}
      currency='CNY'
      moneyRevision='original-money'
      onClose={vi.fn()}
    />
  )
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
  expect(
    screen.getByRole('textbox', { name: 'Prize pool name' })
  ).toBeDisabled()
  expect(screen.getByRole('status')).toHaveTextContent('Core rules are locked')
  const content = document.querySelector('[data-slot=dialog-content]')
  expect(content).toHaveClass('overflow-x-hidden', 'overflow-y-auto')
})

test('chance increase validates supply, shows old and new totals, and submits corrected input', async () => {
  const user = userEvent.setup()
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, data: record } })
  const onClose = vi.fn()
  wrap(<PoolActionDialog record={record} action='append' onClose={onClose} />)
  const input = screen.getByRole('spinbutton', { name: 'Additional chances' })
  fireEvent.change(input, { target: { value: '3' } })
  await user.click(screen.getByRole('button', { name: 'Confirm' }))
  expect(
    await screen.findByText(
      'The requested increase exceeds available prize supply'
    )
  ).toBeVisible()
  expect(post).not.toHaveBeenCalled()
  fireEvent.change(input, { target: { value: '2' } })
  expect(screen.getByText('5')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Confirm' }))
  await waitFor(() => expect(onClose).toHaveBeenCalled())
  expect(post).toHaveBeenCalledWith(
    '/api/marketing/admin/pools/48/append',
    expect.objectContaining({ amount: 2, revision: 2 })
  )
})

test('pause confirmation explains natural expiry and can be dismissed using the keyboard', async () => {
  const user = userEvent.setup()
  const onClose = vi.fn()
  wrap(<PoolActionDialog record={record} action='pause' onClose={onClose} />)
  expect(
    screen.getByRole('alertdialog', { name: 'Pause prize pool' })
  ).toBeVisible()
  expect(
    screen.getByText(
      'Pausing does not consume chances. Existing expiry times continue to apply.'
    )
  ).toBeVisible()
  await user.keyboard('{Escape}')
  await waitFor(() => expect(onClose).toHaveBeenCalled())
})

test('a background refresh cannot reinterpret an open form using a different currency or money revision', async () => {
  const user = userEvent.setup()
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true, data: record } })
  const onClose = vi.fn()
  const view = wrap(
    <PoolEditor
      record={record}
      currency='CNY'
      moneyRevision='original-money'
      onClose={onClose}
    />
  )
  view.rerender(
    <PoolEditor
      record={{ ...record, revision: 9 }}
      currency='USD'
      moneyRevision='changed-money'
      onClose={onClose}
    />
  )
  expect(
    screen.getByRole('textbox', { name: 'Gift amount (CNY)' })
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Save' }))
  await waitFor(() => expect(onClose).toHaveBeenCalled())
  expect(put.mock.calls[0][1]).toMatchObject({
    money_revision: 'original-money',
    revision: 2,
    pool: { prizes: [{ gifts: [{ amount: '2', currency: 'CNY' }] }] },
  })
})

test('archive filters reach list APIs while historical activity and pool selectors include archived records', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { items: [], total: 0, page: 1, page_size: 100 },
    },
  })
  await getPools(2, 20, 'winter', 'archived')
  expect(get).toHaveBeenLastCalledWith('/api/marketing/admin/pools', {
    params: { page: 2, page_size: 20, keyword: 'winter', archive: 'archived' },
  })
  await getAllActivityOptions('winter')
  expect(get).toHaveBeenLastCalledWith('/api/marketing/admin/activities', {
    params: expect.objectContaining({ archive: 'all', keyword: 'winter' }),
  })
  await getPoolOptions(1, '')
  expect(get).toHaveBeenLastCalledWith('/api/marketing/admin/pools', {
    params: expect.objectContaining({ archive: 'all' }),
  })
})

test('archive confirmation preserves user rights and retries failures with the same operation key', async () => {
  const user = userEvent.setup()
  const post = vi
    .spyOn(api, 'post')
    .mockRejectedValueOnce(new Error('Offline'))
    .mockResolvedValueOnce({ data: { success: true, data: record } })
  const onClose = vi.fn()
  wrap(
    <OrganizationActionDialog
      resource='pools'
      record={record}
      action='archive'
      onClose={onClose}
    />
  )
  expect(
    screen.getByText(
      'This only organizes the admin list. Participation, availability, deadlines, and existing user rights remain unchanged.'
    )
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Confirm' }))
  expect(await screen.findByRole('alert')).toHaveTextContent('Offline')
  expect(onClose).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Confirm' }))
  await waitFor(() => expect(onClose).toHaveBeenCalled())
  expect(post).toHaveBeenLastCalledWith(
    '/api/marketing/admin/pools/48/archive',
    expect.objectContaining({ revision: 2 })
  )
  expect(
    (post.mock.calls[0][1] as { operation_key: string }).operation_key
  ).toBe((post.mock.calls[1][1] as { operation_key: string }).operation_key)
})

test('delete confirmation explains draft restrictions and blocks records the server marks ineligible', () => {
  wrap(
    <OrganizationActionDialog
      resource='pools'
      record={{ ...record, deletable: false }}
      action='delete'
      onClose={vi.fn()}
    />
  )
  expect(
    screen.getByText(
      'Only prize pools that have not started, are not referenced by activities, and have no associated records can be deleted. This cannot be undone.'
    )
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Confirm' })).toBeDisabled()
})

test('pool list archive filter keeps search, resets pagination, and disables deletion for used records', async () => {
  const user = userEvent.setup()
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (String(url).endsWith('/metadata')) {
      return {
        data: {
          success: true,
          data: { currency: 'CNY', money_revision: 'rate' },
        },
      }
    }
    return {
      data: {
        success: true,
        data: {
          items: [{ ...record, deletable: false }],
          total: 21,
          page: 1,
          page_size: 20,
        },
      },
    }
  })
  wrap(<MarketingPoolsPage />)
  await screen.findByRole('button', { name: 'Test pool' })
  await user.type(
    screen.getByRole('textbox', { name: 'Search prize pools' }),
    'winter'
  )
  await user.click(screen.getByRole('button', { name: /^Search$/ }))
  await user.click(screen.getByRole('button', { name: 'Go to next page' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/marketing/admin/pools', {
      params: expect.objectContaining({ page: 2, keyword: 'winter' }),
    })
  )
  await user.selectOptions(
    screen.getByRole('combobox', { name: 'Archive status' }),
    'archived'
  )
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/marketing/admin/pools', {
      params: expect.objectContaining({
        page: 1,
        keyword: 'winter',
        archive: 'archived',
      }),
    })
  )
  await user.click(
    screen.getByRole('button', { name: 'Actions for Test pool' })
  )
  expect(screen.getByRole('menuitem', { name: 'Delete' })).toHaveAttribute(
    'aria-disabled',
    'true'
  )
})

test('deleting the only pool on the last page returns to the preceding page', async () => {
  const user = userEvent.setup()
  let deleted = false
  const get = vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
    if (String(url).endsWith('/metadata')) {
      return {
        data: {
          success: true,
          data: { currency: 'CNY', money_revision: 'rate' },
        },
      }
    }
    if (String(url).endsWith('/48')) {
      return { data: { success: true, data: record } }
    }
    const page = (config?.params as { page?: number } | undefined)?.page ?? 1
    return {
      data: {
        success: true,
        data: {
          items: deleted && page === 2 ? [] : [record],
          total: deleted ? 20 : 21,
          page,
          page_size: 20,
        },
      },
    }
  })
  vi.spyOn(api, 'post').mockImplementation(async () => {
    deleted = true
    return { data: { success: true, data: { id: record.id, deleted: true } } }
  })
  wrap(<MarketingPoolsPage />)
  await screen.findByRole('button', { name: 'Test pool' })
  await user.click(screen.getByRole('button', { name: 'Go to next page' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/marketing/admin/pools', {
      params: expect.objectContaining({ page: 2 }),
    })
  )
  await user.click(
    screen.getByRole('button', { name: 'Actions for Test pool' })
  )
  await user.click(screen.getByRole('menuitem', { name: 'Delete' }))
  await user.click(await screen.findByRole('button', { name: 'Confirm' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/marketing/admin/pools', {
      params: expect.objectContaining({ page: 1 }),
    })
  )
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
})
