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
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, AxiosHeaders } from 'axios'
import { toast } from 'sonner'
import { afterEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { GiftBatchDetail } from '../batch-detail'
import { GiftIssueDialog } from '../issue-dialog'
import { giftIssueSchema, giftIssueTotal } from '../schema'

const clients: QueryClient[] = []
afterEach(() => {
  clients.forEach((c) => c.clear())
  clients.length = 0
  vi.restoreAllMocks()
})
function wrap(node: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  return render(
    <QueryClientProvider client={client}>{node}</QueryClientProvider>
  )
}
const batch = {
  id: 8,
  actor_id: 9,
  actor_name: 'admin',
  reason: 'compensation',
  amount: '0.123456789012',
  total_amount: '0.246913578024',
  currency: 'CNY',
  valid_seconds: 3600,
  created_at: 1795708800,
  total: 2,
  credited: 2,
  failed: 0,
  pending: 0,
  recipients: [],
}
function mockUsers() {
  return vi.spyOn(api, 'get').mockImplementation(async (_url, config) => {
    const keyword = String(
      (config?.params as Record<string, unknown> | undefined)?.keyword ?? ''
    )
    const items =
      keyword === 'bob'
        ? [{ id: 2, username: 'bob', display_name: '' }]
        : [{ id: 1, username: 'alice', display_name: '' }]
    return {
      data: {
        success: true,
        data: { items, total: items.length, page: 1, page_size: 20 },
      },
    }
  })
}
async function chooseFirstUser(user: ReturnType<typeof userEvent.setup>) {
  await user.click(
    screen.getByRole('combobox', {
      name: 'Search by username, display name or user ID',
    })
  )
  await user.click(await screen.findByRole('option', { name: /alice/ }))
}
async function chooseUsers(user: ReturnType<typeof userEvent.setup>) {
  await chooseFirstUser(user)
  await user.type(
    screen.getByRole('combobox', {
      name: 'Search by username, display name or user ID',
    }),
    'bob'
  )
  await user.click(await screen.findByRole('option', { name: /bob/ }))
  await user.tab()
}

test('selecting users across searches retains recipients and previews exact per-user and total amounts before posting', async () => {
  mockUsers()
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, data: batch } })
  const user = userEvent.setup()
  const issued = vi.fn()
  wrap(
    <GiftIssueDialog
      currency='CNY'
      moneyRevision='rev1'
      onClose={vi.fn()}
      onIssued={issued}
    />
  )
  await chooseUsers(user)
  await user.type(
    screen.getByLabelText('Gift amount per user (CNY)'),
    '0.123456789012'
  )
  await user.type(
    screen.getByLabelText('Internal issuance reason'),
    'compensation'
  )
  await user.click(
    screen.getByRole('checkbox', { name: 'Permanent gift (no expiry)' })
  )
  fireEvent.change(screen.getByLabelText('Validity after credit'), {
    target: { value: '1' },
  })
  fireEvent.change(screen.getByLabelText('Unit'), {
    target: { value: 'hours' },
  })
  await user.click(screen.getByRole('button', { name: 'Review issuance' }))
  const confirmation = await screen.findByRole('alertdialog')
  expect(within(confirmation).getByText('alice · ID 1')).toBeVisible()
  expect(within(confirmation).getByText('bob · ID 2')).toBeVisible()
  expect(within(confirmation).getByText('¥0.246913578024')).toBeVisible()
  expect(post).not.toHaveBeenCalled()
  await user.click(
    within(confirmation).getByRole('button', { name: 'Confirm issuance' })
  )
  await waitFor(() => expect(issued).toHaveBeenCalledWith(batch))
  expect(post.mock.calls[0]?.[1]).toMatchObject({
    user_ids: [1, 2],
    amount: '0.123456789012',
    currency: 'CNY',
    valid_seconds: 3600,
    reason: 'compensation',
    money_revision: 'rev1',
  })
})

test('a network failure freezes the original form and retries the same issuance key without duplicating the intent', async () => {
  mockUsers()
  const post = vi
    .spyOn(api, 'post')
    .mockRejectedValueOnce(new Error('network failure'))
    .mockResolvedValueOnce({ data: { success: true, data: batch } })
  const user = userEvent.setup()
  const issued = vi.fn()
  wrap(
    <GiftIssueDialog
      currency='CNY'
      moneyRevision='rev1'
      onClose={vi.fn()}
      onIssued={issued}
    />
  )
  await chooseFirstUser(user)
  await user.type(screen.getByLabelText('Gift amount per user (CNY)'), '2.5')
  await user.type(
    screen.getByLabelText('Internal issuance reason'),
    'compensation'
  )
  await user.click(screen.getByRole('button', { name: 'Review issuance' }))
  await user.click(
    within(await screen.findByRole('alertdialog')).getByRole('button', {
      name: 'Confirm issuance',
    })
  )
  await waitFor(() =>
    expect(screen.getByLabelText('Gift amount per user (CNY)')).toBeDisabled()
  )
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  expect(issued).not.toHaveBeenCalled()
  await waitFor(() =>
    expect(
      within(screen.getByRole('alertdialog')).getByRole('button', {
        name: 'Confirm issuance',
      })
    ).toBeEnabled()
  )
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Confirm issuance',
    })
  )
  await waitFor(() => expect(issued).toHaveBeenCalled())
  expect(post.mock.calls[1]?.[1]).toEqual(post.mock.calls[0]?.[1])
  expect(post.mock.calls[1]?.[1]).toHaveProperty('valid_seconds', 0)
})

test('a rejected currency revision shows the server reason and unlocks the form for correction', async () => {
  mockUsers()
  const error = new AxiosError(
    'Request failed',
    'ERR_BAD_REQUEST',
    undefined,
    undefined,
    {
      status: 409,
      statusText: 'Conflict',
      headers: {},
      config: { headers: new AxiosHeaders() },
      data: {
        success: false,
        message: 'Wallet currency changed; refresh the form and try again',
      },
    }
  )
  vi.spyOn(api, 'post').mockRejectedValue(error)
  const notify = vi.spyOn(toast, 'error')
  const user = userEvent.setup()
  wrap(
    <GiftIssueDialog
      currency='CNY'
      moneyRevision='rev1'
      onClose={vi.fn()}
      onIssued={vi.fn()}
    />
  )
  await chooseFirstUser(user)
  await user.type(screen.getByLabelText('Gift amount per user (CNY)'), '2.5')
  await user.type(
    screen.getByLabelText('Internal issuance reason'),
    'compensation'
  )
  await user.click(screen.getByRole('button', { name: 'Review issuance' }))
  await user.click(
    within(await screen.findByRole('alertdialog')).getByRole('button', {
      name: 'Confirm issuance',
    })
  )
  await waitFor(() =>
    expect(notify).toHaveBeenCalledWith(
      'Wallet currency changed; refresh the form and try again'
    )
  )
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(screen.getByLabelText('Gift amount per user (CNY)')).toBeEnabled()
})

test('invalid users, nonpositive amounts and excessive durations cannot be submitted', () => {
  const valid = {
    user_ids: [1, 2],
    amount: '0.000001',
    permanent: true,
    duration: 7,
    unit: 'days' as const,
    reason: 'compensation',
  }
  expect(giftIssueSchema.safeParse(valid).success).toBe(true)
  for (const change of [
    { user_ids: [] },
    { user_ids: Array.from({ length: 101 }, (_, i) => i + 1) },
    { amount: '0' },
    { amount: '-1' },
    { amount: 'NaN' },
    { permanent: false, duration: 0 },
    { permanent: false, duration: 36501 },
    { reason: ' ' },
  ]) {
    expect(giftIssueSchema.safeParse({ ...valid, ...change }).success).toBe(
      false
    )
  }
  expect(giftIssueTotal('9007199254740991.123456789012', 100)).toBe(
    '900719925474099112.345678901200'
  )
})

test('a partial issuance shows each recipient result and retries only through the existing batch', async () => {
  const partial = {
    ...batch,
    credited: 1,
    failed: 1,
    recipients: [
      {
        id: 1,
        user_id: 1,
        username: 'alice',
        state: 'credited',
        updated_at: 1795708800,
        last_actor_id: 9,
        gift: {
          id: 1,
          source_kind: 'admin_gift',
          source_name: '',
          source_name_key: 'Administrator gift',
          currency: 'CNY',
          granted_amount: '0.123456789012',
          remaining_amount: '0.1',
          consumed_amount: '0.023456789012',
          expired_amount: '0',
          cleared_amount: '0',
          credited_at: 1795708800,
          expires_at: 0,
          state: 'active',
        },
      },
      {
        id: 2,
        user_id: 2,
        username: 'bob',
        state: 'failed',
        error_key: 'Recipient is missing or disabled',
        updated_at: 1795708800,
        last_actor_id: 9,
      },
    ],
  }
  vi.spyOn(api, 'get')
    .mockResolvedValueOnce({ data: { success: true, data: partial } })
    .mockResolvedValue({ data: { success: true, data: batch } })
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue({ data: { success: true, data: batch } })
  const user = userEvent.setup()
  wrap(<GiftBatchDetail id={8} onClose={vi.fn()} />)
  expect(
    await screen.findByText('Recipient is missing or disabled')
  ).toBeVisible()
  expect(screen.getByText('alice · ID 1')).toBeVisible()
  expect(screen.getByText('bob · ID 2')).toBeVisible()
  await user.click(
    screen.getByRole('button', { name: 'Retry unsuccessful recipients' })
  )
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/marketing/admin/gift-batches/8/retry',
      {}
    )
  )
  await waitFor(() =>
    expect(
      screen.queryByRole('button', { name: 'Retry unsuccessful recipients' })
    ).not.toBeInTheDocument()
  )
})

test('the recipient search selects server matches by display name or ID and retains chips across result pages', async () => {
  const requests: Record<string, unknown>[] = []
  vi.spyOn(api, 'get').mockImplementation(async (_url, config) => {
    const params = config?.params as Record<string, unknown>
    requests.push(params)
    const second = params.keyword === '2' || Number(params.page) === 2
    const items = second
      ? [{ id: 2, username: 'bob', display_name: '林二' }]
      : [{ id: 1, username: 'alice', display_name: '林一' }]
    return {
      data: {
        success: true,
        data: {
          items,
          total: params.keyword ? 1 : 21,
          page: params.page,
          page_size: 20,
        },
      },
    }
  })
  const user = userEvent.setup()
  wrap(
    <GiftIssueDialog
      currency='CNY'
      moneyRevision='rev1'
      onClose={vi.fn()}
      onIssued={vi.fn()}
    />
  )
  const input = screen.getByRole('combobox', {
    name: 'Search by username, display name or user ID',
  })
  expect(screen.getAllByRole('combobox')).toHaveLength(1)
  await user.type(input, '林一')
  await user.click(await screen.findByRole('option', { name: /alice/ }))
  await waitFor(() => expect(input).toHaveValue(''))
  expect(screen.getByText('alice · ID 1')).toBeVisible()
  await user.type(input, '2')
  await user.click(await screen.findByRole('option', { name: /bob/ }))
  await waitFor(() => expect(input).toHaveValue(''))
  expect(screen.getByText('bob · ID 2')).toBeVisible()
  await user.click(input)
  await user.click(await screen.findByRole('button', { name: 'Next' }))
  await waitFor(() =>
    expect(requests.some((r) => r.keyword === '' && r.page === 2)).toBe(true)
  )
  await screen.findByRole('option', { name: /bob/ })
  await user.keyboard('{Escape}')
  expect(screen.getByText('alice · ID 1')).toBeVisible()
  expect(screen.getByText('bob · ID 2')).toBeVisible()
  expect(screen.getByText('Selected users: 2')).toBeVisible()
})
