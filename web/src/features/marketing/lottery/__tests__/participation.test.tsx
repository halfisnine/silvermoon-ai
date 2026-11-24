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
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { DrawStage } from '../DrawStage'
import { LotteryPage } from '../index'
import type { LotteryDraw, LotteryPool } from '../types'

const pool: LotteryPool = {
  id: 4,
  name: 'Moon gifts',
  state: 'active',
  starts_at: 100,
  ends_at: 4102444800,
  server_now: 1000,
  style: 'wheel',
  available_chances: 2,
  can_draw: true,
  chances: [
    {
      id: 9,
      remaining: 2,
      expires_at: 4102444800,
      state: 'ready',
      source_name: 'Ended source activity',
    },
  ],
  prizes: [
    {
      id: 8,
      name: 'Combined gift',
      available: true,
      state: 'available',
      no_prize: false,
      gifts: [
        { amount: '0.000001', currency: 'CNY', valid_seconds: 6 },
        { amount: '2', currency: 'CNY', valid_seconds: 0 },
      ],
    },
    {
      id: 10,
      name: 'Grand prize',
      available: false,
      state: 'sold_out',
      no_prize: false,
      gifts: [{ amount: '100', currency: 'CNY', valid_seconds: 86400 }],
    },
  ],
}
const outcome: LotteryDraw = {
  id: 88,
  pool_id: 4,
  pool_name: 'Moon gifts',
  prize_id: 8,
  prize_name: 'Combined gift',
  no_prize: false,
  state: 'completed',
  drawn_at: 1001,
  rewards: pool.prizes[0].gifts.map((g) => ({
    ...g,
    credited_at: 1001,
    expires_at: g.valid_seconds ? 1001 + g.valid_seconds : 0,
  })),
}
let userId = 28
let client: QueryClient
let saved: LotteryDraw | null
beforeEach(() => {
  localStorage.clear()
  userId += 1
  saved = null
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  useAuthStore
    .getState()
    .auth.setUser({ id: userId, username: 'lottery-test', role: 1 })
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url.includes('/draws/')) {
      if (saved) return { data: { success: true, data: saved } }
      throw { response: { status: 404 } }
    }
    return {
      data: {
        success: true,
        data: url.endsWith('/pools')
          ? { items: [pool], total: 1, page: 1, page_size: 20 }
          : pool,
      },
    }
  })
})
afterEach(() => {
  client.clear()
  useAuthStore.getState().auth.reset()
  localStorage.clear()
  vi.restoreAllMocks()
})
function mount() {
  return render(
    <QueryClientProvider client={client}>
      <LotteryPage />
    </QueryClientProvider>
  )
}

test.each(['wheel', 'blind_box'] as const)(
  '%s shows immediate drawing feedback while waiting for the server',
  (style) => {
    render(
      <DrawStage
        pool={{ ...pool, style }}
        disabled
        busy
        onDraw={vi.fn()}
        onCloseResult={vi.fn()}
      />
    )
    expect(screen.getByRole('status')).toHaveTextContent('Drawing…')
    expect(
      screen.getByRole('button', {
        name: style === 'wheel' ? 'Spin once' : 'Open mystery box',
      })
    ).toBeDisabled()
    if (style === 'blind_box') {
      expect(
        document.querySelector('.silvermoon-lottery-box-button svg')
      ).toHaveClass('size-full')
    }
  }
)

test('wheel segments and prize list show prize names without numbered labels', async () => {
  mount()
  const wheel = await screen.findByRole('img', { name: 'Prize wheel' })
  expect(
    Array.from(wheel.querySelectorAll('text'), (label) => label.textContent)
  ).toEqual(['Combined gift', 'Grand prize'])
  const list = screen.getByRole('region', { name: 'Prize list' })
  expect(
    Array.from(list.querySelectorAll('h4'), (title) => title.textContent)
  ).toEqual(['Combined gift', 'Grand prize'])
  for (const article of list.querySelectorAll('article')) {
    expect(article.querySelector('div')?.firstElementChild?.tagName).toBe('H4')
  }
})

test('server outcome survives a lost response and navigation without a second draw', async () => {
  const user = userEvent.setup()
  const post = vi.spyOn(api, 'post').mockImplementation(async () => {
    saved = outcome
    throw new Error('Response lost')
  })
  const view = mount()
  await screen.findByRole('button', { name: 'Spin once' })
  await user.click(screen.getByRole('button', { name: 'Spin once' }))
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  await waitFor(() =>
    expect(
      localStorage.getItem(`silvermoon:lottery:v1:${userId}:4`)
    ).toBeTruthy()
  )
  view.unmount()
  client.clear()
  mount()
  expect(
    await screen.findByRole('dialog', { name: 'Your draw result' })
  ).toBeVisible()
  expect(post).toHaveBeenCalledTimes(1)
  expect(screen.getByRole('dialog')).toHaveTextContent('0.000001')
  expect(screen.getByRole('dialog')).toHaveTextContent('6 seconds')
  await user.click(screen.getAllByRole('button', { name: 'Close' })[0])
  expect(localStorage.getItem(`silvermoon:lottery:v1:${userId}:4`)).toBeNull()
})

test('double clicks post once and a failed attempt is retried with the original key', async () => {
  const user = userEvent.setup()
  let reject!: (reason: Error) => void
  const post = vi
    .spyOn(api, 'post')
    .mockImplementationOnce(
      () =>
        new Promise((_resolve, rejectPromise) => {
          reject = rejectPromise
        })
    )
    .mockImplementationOnce(async () => {
      saved = outcome
      return { data: { success: true, data: outcome } }
    })
  mount()
  const button = await screen.findByRole('button', { name: 'Spin once' })
  fireEvent.click(button)
  fireEvent.click(button)
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  await act(async () => reject(new Error('Offline')))
  const retry = await screen.findByRole('button', { name: 'Retry this draw' })
  await user.click(retry)
  expect(
    await screen.findByRole('dialog', { name: 'Your draw result' })
  ).toBeVisible()
  expect(post).toHaveBeenCalledTimes(2)
  expect(post.mock.calls[0][1]).toEqual(post.mock.calls[1][1])
  await user.click(screen.getAllByRole('button', { name: 'Close' })[0])
})

test('pending partial gifts never claim all credited and closing the dialog preserves recovery', async () => {
  saved = {
    ...outcome,
    state: 'pending',
    rewards: [outcome.rewards[0], { ...pool.prizes[0].gifts[1] }],
  }
  localStorage.setItem(`silvermoon:lottery:v1:${userId}:4`, 'pending-original')
  const post = vi.spyOn(api, 'post')
  mount()
  const dialog = await screen.findByRole('dialog', {
    name: 'Your draw result',
  })
  expect(dialog).toHaveTextContent('Pending delivery')
  expect(dialog).not.toHaveTextContent(
    'All gifts from this draw have been credited.'
  )
  await userEvent.setup().keyboard('{Escape}')
  expect(localStorage.getItem(`silvermoon:lottery:v1:${userId}:4`)).toBe(
    'pending-original'
  )
  expect(post).not.toHaveBeenCalled()
  expect(screen.getByRole('button', { name: 'Spin once' })).toBeDisabled()
})

test('paused pools preserve chance deadlines and clearly show sold-out prizes without a draw action', async () => {
  vi.mocked(api.get).mockImplementation(async (url) => ({
    data: {
      success: true,
      data: url.endsWith('/pools')
        ? {
            items: [{ ...pool, state: 'paused', can_draw: false }],
            total: 1,
            page: 1,
            page_size: 20,
          }
        : { ...pool, state: 'paused', can_draw: false },
    },
  }))
  mount()
  expect(
    await screen.findByRole('button', { name: 'Spin once' })
  ).toBeDisabled()
  expect(screen.getByText('Sold out')).toBeVisible()
  expect(screen.getByText('Ended source activity')).toBeVisible()
  expect(
    screen.getByText(
      'Draws are paused. Existing chances continue to expire at their original deadlines.'
    )
  ).toBeVisible()
})

test('blind box reveals a backend no-prize outcome under reduced motion without credit claims', async () => {
  const close = vi.fn()
  render(
    <DrawStage
      pool={{ ...pool, style: 'blind_box' }}
      result={{
        ...outcome,
        no_prize: true,
        prize_name: 'Thank you',
        rewards: [],
      }}
      disabled
      busy={false}
      onDraw={vi.fn()}
      onCloseResult={close}
    />
  )
  const dialog = await screen.findByRole('dialog', { name: 'No prize' })
  expect(dialog).toHaveTextContent(
    'This draw used one chance. No gift was awarded.'
  )
  expect(dialog).not.toHaveTextContent('credited')
  await userEvent.setup().keyboard('{Escape}')
  expect(close).toHaveBeenCalledTimes(1)
})

test('empty pools and a failed list offer honest empty and retry states', async () => {
  vi.mocked(api.get)
    .mockRejectedValueOnce(new Error('Offline'))
    .mockResolvedValue({
      data: {
        success: true,
        data: { items: [], total: 0, page: 1, page_size: 20 },
      },
    })
  mount()
  await userEvent
    .setup()
    .click(await screen.findByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('No prize pools available')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Spin once' })
  ).not.toBeInTheDocument()
})

test('leaving during a live request recovers its server result without starting another draw', async () => {
  let finish: () => void = () => undefined
  const post = vi.spyOn(api, 'post').mockImplementation(
    () =>
      new Promise((resolve) => {
        saved = outcome
        finish = () => resolve({ data: { success: true, data: outcome } })
      })
  )
  const view = mount()
  await userEvent
    .setup()
    .click(await screen.findByRole('button', { name: 'Spin once' }))
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  const operation = localStorage.getItem(`silvermoon:lottery:v1:${userId}:4`)
  view.unmount()
  mount()
  expect(
    await screen.findByRole('dialog', { name: 'Your draw result' })
  ).toHaveTextContent('Combined gift')
  await act(async () => finish())
  expect(post).toHaveBeenCalledTimes(1)
  expect(localStorage.getItem(`silvermoon:lottery:v1:${userId}:4`)).toBe(
    operation
  )
})

test('forbidden pool details expose no private pool or draw action', async () => {
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url.endsWith('/pools')) {
      return {
        data: {
          success: true,
          data: { items: [pool], total: 1, page: 1, page_size: 20 },
        },
      }
    }
    throw { response: { status: 403, data: { message: 'Access denied' } } }
  })
  const post = vi.spyOn(api, 'post')
  mount()
  expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Spin once' })
  ).not.toBeInTheDocument()
  expect(screen.queryByText('Combined gift')).not.toBeInTheDocument()
  expect(post).not.toHaveBeenCalled()
})
