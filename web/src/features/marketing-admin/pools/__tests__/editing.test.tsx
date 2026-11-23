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

import { PoolActionDialog } from '../PoolActionDialog'
import { PoolEditor } from '../PoolEditor'
import type { PoolRecord } from '../types'

const future = Math.floor(Date.now() / 1000) + 86400
const record: PoolRecord = {
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
