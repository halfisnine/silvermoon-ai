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
  expect(
    screen.getByText(
      'This opportunity limit is shared by all linked activities.'
    )
  ).toBeVisible()
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
test('activity center loads every page for scrolling and reloads the complete filtered list', async () => {
  const get = vi.spyOn(api, 'get').mockImplementation(async (_url, config) => {
    const params = config?.params as { page: number; kind: string }
    const filtered = params.kind === 'invitation'
    const count = filtered ? 21 : 22
    const offset = (params.page - 1) * 20
    const items = Array.from(
      { length: Math.min(20, count - offset) },
      (_, i) => ({
        ...activity,
        id: offset + i + 1,
        activity: {
          ...activity.activity,
          name: `${filtered ? 'Invitation' : 'Recharge'} ${offset + i + 1}`,
          kind: filtered ? 'invitation' : 'recharge',
        },
      })
    )
    return envelope({ ...page(items), total: count, page: params.page })
  })
  await view(<ActivityCenterPage />)
  expect(await screen.findByText('Recharge 22')).toBeVisible()
  expect(screen.getAllByRole('heading', { level: 3 })).toHaveLength(22)
  expect(
    screen.queryByRole('navigation', { name: 'Pagination' })
  ).not.toBeInTheDocument()
  fireEvent.change(screen.getByRole('combobox', { name: 'Activity type' }), {
    target: { value: 'invitation' },
  })
  expect(await screen.findByText('Invitation 21')).toBeVisible()
  expect(screen.queryByText('Recharge 22')).not.toBeInTheDocument()
  expect(screen.getAllByRole('heading', { level: 3 })).toHaveLength(21)
  expect(get).toHaveBeenCalledWith('/api/marketing/user/activities', {
    params: { page: 2, page_size: 20, kind: 'invitation', state: '' },
  })
})
test('a later activity page failure is retryable without silently truncating the list', async () => {
  let fail = true
  vi.spyOn(api, 'get').mockImplementation(async (_url, config) => {
    const params = config?.params as { page: number } | undefined
    const current = params?.page ?? 1
    if (current === 2 && fail) throw new Error('Second page unavailable')
    return envelope({
      ...page([
        {
          ...activity,
          id: current,
          activity: { ...activity.activity, name: `Page ${current}` },
        },
      ]),
      total: 2,
      page: current,
      page_size: 1,
    })
  })
  await view(<ActivityCenterPage />)
  const retry = await screen.findByRole('button', { name: 'Retry' })
  expect(screen.queryByText('Page 1')).not.toBeInTheDocument()
  fail = false
  fireEvent.click(retry)
  expect(await screen.findByText('Page 2')).toBeVisible()
  expect(screen.getByText('Page 1')).toBeVisible()
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
test('calendar distinguishes signed, make-up, future and today cells and signs from today directly', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) =>
    envelope(url === '/api/user/checkin' ? checkin : {})
  )
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue(
      envelope({ date: checkin.today, rewards_pending: false })
    )
  await view(<CheckinPage />)
  expect(
    await screen.findByRole('button', { name: /November 21.*Checked in/ })
  ).toBeDisabled()
  expect(
    screen.getByRole('button', { name: /November 22.*Make-up available/ })
  ).toBeEnabled()
  expect(
    screen.getByRole('button', { name: /November 24.*Not yet available/ })
  ).toBeDisabled()
  const today = screen.getByRole('button', {
    name: /November 23.*Check in now/,
  })
  expect(today).toHaveAttribute('aria-current', 'date')
  expect(today).toBeEnabled()
  await userEvent.setup().click(today)
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith('/api/user/checkin', {}, { params: {} })
  )
})

test('calendar arrow keys move from today to a make-up date and Enter opens confirmation', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) =>
    envelope(url === '/api/user/checkin' ? checkin : {})
  )
  await view(<CheckinPage />)
  const today = await screen.findByRole('button', {
    name: /November 23.*Check in now/,
  })
  act(() => today.focus())
  const user = userEvent.setup()
  await user.keyboard('{ArrowLeft}')
  expect(
    screen.getByRole('button', { name: /November 22.*Make-up available/ })
  ).toHaveFocus()
  await user.keyboard('{Enter}')
  expect(await screen.findByRole('alertdialog')).toHaveTextContent(
    'Make up check-in for'
  )
})

test('available actions take priority when another activity already recorded the same date', async () => {
  const mixed = {
    ...checkin,
    checked_in_today: true,
    activities: [
      {
        ...checkin.activities[0],
        id: 1,
        makeup_dates: [],
        records: [
          { date: '2026-11-22', makeup: false },
          { date: checkin.today, makeup: false },
        ],
      },
      { ...checkin.activities[0], id: 2, name: 'Second check-in', records: [] },
    ],
  }
  vi.spyOn(api, 'get').mockImplementation(async (url) =>
    envelope(url === '/api/user/checkin' ? mixed : {})
  )
  await view(<CheckinPage />)
  const past = await screen.findByRole('button', {
    name: /November 22.*Make-up available/,
  })
  expect(past).toBeEnabled()
  expect(
    screen.getByRole('button', { name: /November 23.*Check in now/ })
  ).toBeEnabled()
  expect(screen.getByRole('button', { name: 'Check in now' })).toBeEnabled()
  fireEvent.click(past)
  expect(await screen.findByRole('alertdialog')).toHaveTextContent(
    'Make up check-in for'
  )
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
test('invitation without public campaigns preserves link and friends but offers no balance transfer', async () => {
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
  expect(
    await screen.findByText('No invitation activities to display')
  ).toBeVisible()
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

test('invited friends use the shared table pagination and preserve the invitation link across pages', async () => {
  const get = vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
    if (url === '/api/marketing/user/invitation') {
      const page =
        (config?.params as Record<string, number> | undefined)?.page ?? 1
      return envelope({
        invitation_path: '/sign-up?aff=alice',
        friend_count: 21,
        friends: {
          items:
            page === 1
              ? [{ username: 'First friend', joined_at: 1795132800 }]
              : [{ username: 'Last friend', joined_at: 1795132800 }],
          total: 21,
          page,
          page_size: 20,
        },
      })
    }
    return envelope({ items: [], total: 0, page: 1, page_size: 20 })
  })
  await view(<InvitationPage />)
  expect(
    await screen.findByRole('columnheader', { name: 'Username' })
  ).toBeVisible()
  expect(
    screen.getByRole('columnheader', { name: 'Registration time' })
  ).toBeVisible()
  expect(await screen.findByText('First friend')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Previous' })
  ).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Next' })).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Go to next page' }))
  expect(await screen.findByText('Last friend')).toBeVisible()
  expect(
    screen.getByRole('textbox', { name: 'Your invitation link' })
  ).toHaveValue(`${window.location.origin}/sign-up?aff=alice`)
  expect(get).toHaveBeenCalledWith('/api/marketing/user/invitation', {
    params: { page: 2, page_size: 20 },
  })
  expect(screen.getByRole('button', { name: 'Go to next page' })).toBeDisabled()
  await userEvent.click(
    screen.getByRole('button', { name: 'Go to previous page' })
  )
  expect(await screen.findByText('First friend')).toBeVisible()
})

test('invitation with no friends shows a table empty state without redundant pagination', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) =>
    envelope(
      url === '/api/marketing/user/invitation'
        ? {
            invitation_path: '/sign-up?aff=alice',
            friend_count: 0,
            friends: { items: [], total: 0, page: 1, page_size: 20 },
          }
        : { items: [], total: 0, page: 1, page_size: 20 }
    )
  )
  await view(<InvitationPage />)
  expect(await screen.findByText('No invited friends yet')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Go to next page' })
  ).not.toBeInTheDocument()
  expect(
    screen.getByRole('textbox', { name: 'Your invitation link' })
  ).toBeVisible()
})

test.each([
  ['active', 'Ongoing'],
  ['paused', 'Paused'],
  ['scheduled', 'Upcoming'],
  ['ended', 'Ended'],
])(
  'invitation only shows ongoing rules while preserving friend history for %s campaigns',
  async (state, label) => {
    const invitationActivity = {
      ...activity,
      state,
      activity: {
        ...activity.activity,
        name: 'Invite a friend',
        kind: 'invitation',
        condition: 'friend_registration',
        frequency: 'once',
      },
    }
    const get = vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
      if (url === '/api/marketing/user/invitation') {
        return envelope({
          invitation_path: '/sign-up?aff=alice',
          friend_count: 1,
          friends: page([
            {
              username: 'friend',
              joined_at: 1795132800,
              rewards: { granted: 1, pending: 0, other: 0 },
            },
          ]),
        })
      }
      const filter = config?.params as { state?: string } | undefined
      return envelope(
        page(
          filter?.state && filter.state !== state ? [] : [invitationActivity]
        )
      )
    })
    await view(<InvitationPage />)
    if (state === 'active') {
      expect(
        await screen.findByRole('heading', { name: 'Invite a friend' })
      ).toBeVisible()
      expect(screen.getByText(label, { exact: true })).toBeVisible()
      expect(
        screen.getByText('A friend registers during this activity.')
      ).toBeVisible()
      expect(screen.getByText(/0.00012/)).toHaveTextContent('Validity: 3 days')
    } else {
      expect(
        await screen.findByText('No invitation activities to display')
      ).toBeVisible()
      expect(
        screen.queryByRole('heading', { name: 'Invite a friend' })
      ).not.toBeInTheDocument()
    }
    expect(screen.getByText('friend', { exact: true })).toBeVisible()
    expect(screen.getByText('Granted reward items: 1')).toBeVisible()
    expect(
      screen.getByRole('link', { name: 'View rewards for friend' })
    ).toHaveAttribute(
      'href',
      '/activities/rewards?tab=records&source=invitation&friend=friend'
    )
    expect(get).toHaveBeenCalledWith('/api/marketing/user/invitation', {
      params: { page: 1, page_size: 20 },
    })
    expect(get).toHaveBeenCalledWith('/api/marketing/user/activities', {
      params: { page: 1, page_size: 20, kind: 'invitation', state: 'active' },
    })
  }
)

test('invitation rows distinguish granted, pending, other and absent rewards and link to that friend only', async () => {
  vi.spyOn(api, 'get').mockImplementation(async (url) =>
    envelope(
      url === '/api/marketing/user/invitation'
        ? {
            invitation_path: '/sign-up?aff=alice',
            friend_count: 2,
            friends: page([
              {
                username: 'friend',
                joined_at: 1795132800,
                rewards: { granted: 2, pending: 1, other: 1 },
              },
              {
                username: 'new-friend',
                joined_at: 1795132800,
                rewards: { granted: 0, pending: 0, other: 0 },
              },
            ]),
          }
        : page([])
    )
  )
  await view(<InvitationPage />)
  expect(await screen.findByText('Granted reward items: 2')).toBeVisible()
  expect(screen.getByText('Pending reward items: 1')).toBeVisible()
  expect(screen.getByText('Other reward results: 1')).toBeVisible()
  expect(screen.getByText('No invitation reward records yet')).toBeVisible()
  const href =
    screen
      .getByRole('link', { name: 'View rewards for friend' })
      .getAttribute('href') ?? ''
  const target = new URL(href, window.location.origin)
  expect(target.pathname).toBe('/activities/rewards')
  expect(target.searchParams.get('source')).toBe('invitation')
  expect(target.searchParams.get('friend')).toBe('friend')
  expect(
    screen.queryByRole('link', { name: 'View rewards for new-friend' })
  ).not.toBeInTheDocument()
})

test('friend reward view requests only invitation records for that friend and can clear the filter', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue(envelope(page([])))
  await view(<ActivityRewardsPage source='invitation' friend='friend' />)
  await screen.findByText('No reward records found')
  expect(get).toHaveBeenLastCalledWith('/api/marketing/user/rewards', {
    params: expect.objectContaining({
      kind: 'activity',
      activity_kind: 'invitation',
      friend_username: 'friend',
    }),
  })
  expect(screen.getByText('friend', { exact: true })).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: 'Clear' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/marketing/user/rewards', {
      params: expect.not.objectContaining({ friend_username: 'friend' }),
    })
  )
})
