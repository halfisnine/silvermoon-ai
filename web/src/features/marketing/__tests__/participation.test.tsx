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
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import type { ReactNode } from 'react'
import { beforeEach, afterEach, expect, test, vi } from 'vitest'

import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { ActivityCenterPage } from '../center'
import { CheckinPage } from '../checkin'
import { WalletActivityNotice } from '../components/wallet-activity-notice'
import { InvitationPage } from '../invitation'
import { ActivityRewardsPage } from '../rewards'

let client: QueryClient
beforeEach(() => {
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  client.setQueryData(['status'], { turnstile_check: false })
  useAuthStore.getState().auth.setUser({ id: 1, username: 'alice', role: 1 })
})
afterEach(async () => {
  await act(() => i18next.changeLanguage('en'))
  vi.useRealTimers()
  client.clear()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})
async function view(node: ReactNode) {
  const router = createRouter({
    routeTree: createRootRoute({ component: () => node }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  return render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}
const reward = {
  kind: 'gift',
  calculation: 'fixed',
  amount: '0.00012',
  currency: 'CNY',
  valid_seconds: 259200,
}
const activity = {
  id: 1,
  state: 'active',
  server_now: 1795392000,
  activity: {
    name: 'November recharge',
    kind: 'recharge',
    condition: 'cumulative_payments',
    frequency: 'every',
    starts_at: 1795132800,
    ends_at: 1795996800,
    threshold_cny: '100',
    rewards: [reward],
    checkin: null,
  },
  pools: [
    {
      id: 8,
      name: 'Winter wheel',
      state: 'active',
      starts_at: 1795132800,
      ends_at: 1795996800,
      capacity: 100,
      remaining_capacity: 20,
    },
  ],
}
const page = (items: unknown[]) => ({
  items,
  total: items.length,
  page: 1,
  page_size: 20,
})
const envelope = (data: unknown) => ({ data: { success: true, data } })

test('activity center shows structured actual-payment rules and only associated pool supply', async () => {
  vi.spyOn(api, 'get').mockResolvedValue(envelope(page([activity])))
  await view(<ActivityCenterPage />)
  expect(await screen.findByText('November recharge')).toBeVisible()
  expect(
    screen.getByText('Payments accumulate from the start of this activity.')
  ).toBeVisible()
  expect(screen.getByText(/20 remaining out of 100/)).toBeVisible()
  expect(screen.getByText('This opportunity limit is shared by all linked activities.')).toBeVisible()
  expect(screen.getByText(/0.00012/)).toHaveTextContent('Validity: 3 days')
  expect(screen.getByRole('link', { name: 'Go to wallet' })).toHaveAttribute(
    'href',
    '/wallet'
  )
  expect(document.body).not.toHaveTextContent('500000')
})
test('ended activity cannot offer recharge but preserves a link to existing draw opportunities', async () => {
  vi.spyOn(api, 'get').mockResolvedValue(
    envelope(page([{ ...activity, state: 'ended' }]))
  )
  await view(<ActivityCenterPage />)
  await screen.findByText('November recharge')
  expect(
    screen.queryByRole('link', { name: 'Go to wallet' })
  ).not.toBeInTheDocument()
  expect(
    screen.getByRole('link', { name: 'View draw opportunities' })
  ).toBeVisible()
  expect(
    screen.getByText(
      'This activity has ended. Existing rewards keep their own validity.'
    )
  ).toBeVisible()
})
test('wallet without an active recharge activity displays no invented reward promise', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue(envelope(page([])))
  const rendered = await view(<WalletActivityNotice />)
  await waitFor(() => expect(get).toHaveBeenCalled())
  expect(rendered.container).not.toHaveTextContent(
    'Recharge activities are available.'
  )
  expect(
    screen.queryByRole('link', { name: 'View activity details' })
  ).not.toBeInTheDocument()
})
test('activity center loading failure exposes Retry and recovers to a truthful empty state', async () => {
  vi.spyOn(api, 'get')
    .mockRejectedValueOnce(new Error('Network'))
    .mockResolvedValue(envelope(page([])))
  await view(<ActivityCenterPage />)
  fireEvent.click(await screen.findByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('No activities to display')).toBeVisible()
})
const checkin = {
  month: '2026-11',
  today: '2026-11-23',
  server_now: 1795392000,
  can_checkin: true,
  checked_in_today: false,
  activities: [
    {
      id: 1,
      name: 'November check-in',
      state: 'active',
      starts_at: 1795132800,
      ends_at: 1795996800,
      paused: false,
      condition: 'none',
      required_payment_cny: '0',
      cycle_days: 7,
      makeup_days: 3,
      makeup_limit: 2,
      makeup_daily: true,
      makeup_used: 1,
      makeup_remaining: 1,
      makeup_dates: ['2026-11-22'],
      streak: 6,
      today_eligibility: 'eligible',
      daily_rewards: [reward],
      milestones: [{ days: 7, rewards: [{ ...reward, amount: '2' }] }],
      records: [{ date: '2026-11-21', makeup: false }],
    },
  ],
}
test('check-in displays daily plus extra rewards and submits without selecting an activity', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) =>
    envelope(url === '/api/user/checkin' ? checkin : {})
  )
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue(envelope({ date: '2026-11-23', rewards_pending: true }))
  await view(<CheckinPage />)
  expect(await screen.findByText('November check-in')).toBeVisible()
  expect(
    screen.getByText(
      'Daily and extra streak rewards are added together on a milestone day.'
    )
  ).toBeVisible()
  expect(
    screen.getByText(/1 make-ups remaining in this activity/)
  ).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: 'Check in now' }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith('/api/user/checkin', {}, { params: {} })
  )
  expect(
    screen.getByRole('link', { name: 'View check-in rewards' })
  ).toHaveAttribute('href', expect.stringContaining('/activities/rewards'))
})
test('no check-in activity keeps reward history navigation and disables daily submission', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) =>
    envelope(
      url === '/api/user/checkin'
        ? { ...checkin, activities: [], can_checkin: false }
        : {}
    )
  )
  await view(<CheckinPage />)
  expect(
    await screen.findByText('No check-in activity for this period')
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Check in now' })).toBeDisabled()
  expect(
    screen.getByRole('link', { name: 'View check-in rewards' })
  ).toBeVisible()
})
test('invitation without an active campaign preserves link and friends but offers no balance transfer', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) =>
    envelope(
      url === '/api/marketing/user/invitation'
        ? {
            invitation_code: 'alice',
            invitation_path: '/sign-up?aff=alice',
            friend_count: 1,
            friends: page([{ username: 'friend', joined_at: 1795132800 }]),
          }
        : page([])
    )
  )
  await view(<InvitationPage />)
  expect(await screen.findByText('No active invitation activity')).toBeVisible()
  expect(await screen.findByText('friend')).toBeVisible()
  expect(
    screen.getByRole('textbox', { name: 'Your invitation link' })
  ).toHaveValue(`${window.location.origin}/sign-up?aff=alice`)
  expect(screen.queryByText('Transfer')).not.toBeInTheDocument()
})
test('rewards show shortage separately from credited gifts and keep gift details in the same page', async () => {
  const record = {
    id: 1,
    kind: 'activity',
    source_name: 'Chance limit',
    state: 'shortage',
    occurred_at: 1795132800,
    credited_at: 0,
    rewards: [{ kind: 'chance', pool_name: 'Winter wheel', count: 3 }],
    source: {},
  }
  const gift = {
    id: 2,
    source_name: 'Daily check-in',
    source_kind: 'activity',
    currency: 'CNY',
    granted_amount: '2',
    consumed_amount: '0.5',
    remaining_amount: '0.2',
    expired_amount: '1.3',
    cleared_amount: '0',
    credited_at: 1795132800,
    expires_at: 1795132860,
    state: 'expired',
  }
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/marketing/user/gifts/2') return envelope(gift)
    if (url === '/api/marketing/user/gifts') return envelope(page([gift]))
    return envelope(page([record]))
  })
  await view(<ActivityRewardsPage />)
  expect(await screen.findByText('Opportunity limit reached')).toBeVisible()
  expect(screen.queryByText('Granted')).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('tab', { name: 'Gift details' }))
  expect(await screen.findByText('Daily check-in')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: 'Details' }))
  const dialog = await screen.findByRole('dialog')
  expect(await within(dialog).findByText('Expired unused gift')).toBeVisible()
  expect(
    within(dialog).getByText(
      'A request started before expiry is still settling. Its final gift consumption will appear here; expired gifts cannot fund new requests.'
    )
  ).toBeVisible()
  expect(
    within(dialog).getByText(
      'Expired or cleared gifts are not counted as consumption.'
    )
  ).toBeVisible()
})
test('changing the signed-in user never reuses another account’s reward query data', async () => {
  let source = 'Alice reward'
  vi.spyOn(api, 'get').mockImplementation(async () =>
    envelope(
      page([
        {
          id: 1,
          kind: 'activity',
          source_name: source,
          state: 'credited',
          occurred_at: 1795132800,
          credited_at: 1795132800,
          rewards: [reward],
          source: {},
        },
      ])
    )
  )
  await view(<ActivityRewardsPage />)
  await screen.findByText('Alice reward')
  source = 'Bob reward'
  await act(async () =>
    useAuthStore.getState().auth.setUser({ id: 2, username: 'bob', role: 1 })
  )
  expect(await screen.findByText('Bob reward')).toBeVisible()
  expect(screen.queryByText('Alice reward')).not.toBeInTheDocument()
})

test('make-up check-in sends only the chosen eligible date and keeps activity selection on the server', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) =>
    envelope(url === '/api/user/checkin' ? checkin : {})
  )
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue(
      envelope({ date: '2026-11-22', makeup: true, rewards_pending: false })
    )
  await view(<CheckinPage />)
  await screen.findByText('November check-in')
  const day = screen.getByRole('button', { name: /November 22/ })
  expect(day).toBeEnabled()
  fireEvent.click(day)
  const confirm = await screen.findByRole('alertdialog')
  fireEvent.click(
    within(confirm).getByRole('button', { name: 'Make up check-in' })
  )
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/user/checkin/makeup',
      { date: '2026-11-22' },
      { params: {} }
    )
  )
})

test('reward source and inclusive calendar-day filters apply together, including a one-day range', async () => {
  const user = userEvent.setup()
  vi.setSystemTime(new Date('2026-11-23T10:00:00Z'))
  const get = vi.spyOn(api, 'get').mockResolvedValue(envelope(page([])))
  await view(<ActivityRewardsPage />)
  await screen.findByText('No reward records found')
  fireEvent.change(screen.getByLabelText('Reward source'), {
    target: { value: 'checkin' },
  })
  await user.click(screen.getByLabelText('From'))
  await user.click(await screen.findByRole('button', { name: /November 22/ }))
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  await user.click(screen.getByLabelText('Until'))
  await user.click(await screen.findByRole('button', { name: /November 22/ }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/marketing/user/rewards', {
      params: expect.objectContaining({
        kind: 'activity',
        activity_kind: 'checkin',
        starts_at: Date.parse('2026-11-22T00:00:00+08:00') / 1000,
        ends_at: Date.parse('2026-11-23T00:00:00+08:00') / 1000,
      }),
    })
  )
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(screen.queryByLabelText('Time')).not.toBeInTheDocument()
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  await user.click(screen.getByLabelText('Until'))
  await user.click(await screen.findByRole('button', { name: /November 21/ }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'End date must be on or after start date'
  )
})

test('switching interface language updates activity labels, money and Chinese month-day dates', async () => {
  i18next.addResourceBundle('zhCN', 'translation', zh.translation)
  vi.spyOn(api, 'get').mockResolvedValue(envelope(page([activity])))
  await view(<ActivityCenterPage />)
  await screen.findByText('November recharge')
  await act(() => i18next.changeLanguage('zhCN'))
  expect(screen.getByText('参与条件')).toBeVisible()
  expect(screen.getAllByText(/2026年11月20日/)[0]).toBeVisible()
  expect(screen.getByText(/0.00012/)).toHaveTextContent('有效期')
  await act(() => i18next.changeLanguage('en'))
  expect(screen.getByText('Participation conditions')).toBeVisible()
})

test('a fixed-gift-only recharge campaign does not imply it awards lottery opportunities', async () => {
  vi.spyOn(api, 'get').mockResolvedValue(
    envelope(page([{ ...activity, pools: [] }]))
  )
  await view(<WalletActivityNotice />)
  await screen.findByText(
    'Recharge activities are available. Rewards depend on the activity rules.'
  )
  expect(
    screen.queryByText(
      'Draw opportunities are limited. Payment does not reserve a place.'
    )
  ).not.toBeInTheDocument()
})
