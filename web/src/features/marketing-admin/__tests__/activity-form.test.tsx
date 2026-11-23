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
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { ActivityEditor } from '../activities/editor'
import {
  activitySchema,
  changeActivityCondition,
  defaultActivity,
} from '../activities/schema'
import type { ActivityDetail } from '../types'

const initial: ActivityDetail = {
  id: 1,
  revision: 2,
  money_revision: 'rate-a',
  state: 'draft',
  enabled_at: 0,
  editable: true,
  activity: {
    ...defaultActivity('CNY'),
    name: 'Registration welcome',
    starts_at: 2000000000,
    rewards: [
      {
        kind: 'gift',
        calculation: 'fixed',
        amount: '0.000002',
        currency: 'CNY',
        valid_seconds: 259200,
      },
    ],
  },
}
const clients: QueryClient[] = []
function show(original = initial) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  const onClose = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <ActivityEditor
        currency='CNY'
        moneyRevision='rate-a'
        original={original}
        onClose={onClose}
      />
    </QueryClientProvider>
  )
  return onClose
}
beforeEach(() => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { items: [], total: 0, page: 1, page_size: 20 },
    },
  })
})
afterEach(() => {
  clients.splice(0).forEach((c) => c.clear())
  vi.restoreAllMocks()
})

describe('activity editing', () => {
  test('a corrected client validation error can be saved without closing the form', async () => {
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true, data: initial } })
    const onClose = show()
    fireEvent.change(screen.getByLabelText('Gift amount (CNY)'), {
      target: { value: '-2' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save draft' }))
    expect(await screen.findByText('Enter a positive amount')).toBeVisible()
    expect(put).not.toHaveBeenCalled()
    fireEvent.change(screen.getByLabelText('Gift amount (CNY)'), {
      target: { value: '0.000003' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Save draft' }))
    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(put.mock.calls[0][1]).toMatchObject({
      money_revision: 'rate-a',
      revision: 2,
      activity: { rewards: [{ amount: '0.000003' }] },
    })
  })
  test('a failed request retries the same operation, while a corrected payload gets a new operation', async () => {
    const put = vi
      .spyOn(api, 'put')
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce({
        data: { success: false, message: 'Invalid marketing configuration' },
      })
      .mockResolvedValueOnce({ data: { success: true, data: initial } })
    const onClose = show()
    const save = screen.getByRole('button', { name: 'Save draft' })
    fireEvent.click(save)
    await screen.findByRole('alert')
    fireEvent.click(save)
    await screen.findByText('Invalid marketing configuration')
    const first = put.mock.calls[0][1] as { operation_key: string }
    const retry = put.mock.calls[1][1] as { operation_key: string }
    expect(retry.operation_key).toBe(first.operation_key)
    fireEvent.change(screen.getByLabelText('Activity name'), {
      target: { value: 'Corrected registration' },
    })
    fireEvent.click(save)
    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(
      (put.mock.calls[2][1] as { operation_key: string }).operation_key
    ).not.toBe(first.operation_key)
  })
  test('choosing cumulative recharge removes percentage gift configuration', async () => {
    show({
      ...initial,
      activity: {
        ...initial.activity,
        kind: 'recharge',
        condition: 'single_payment',
        frequency: 'every',
        threshold_cny: '1',
        rewards: [
          {
            kind: 'gift',
            calculation: 'paid_ratio',
            ratio_percent: '5',
            currency: 'CNY',
            valid_seconds: 0,
          },
        ],
      },
    })
    expect(screen.getByLabelText('Gift percentage (%)')).toBeVisible()
    fireEvent.change(screen.getByLabelText('Reward trigger'), {
      target: { value: 'cumulative_payments' },
    })
    expect(
      screen.queryByLabelText('Gift percentage (%)')
    ).not.toBeInTheDocument()
    expect(screen.getByLabelText('Gift amount (CNY)')).toHaveValue('')
    fireEvent.click(
      screen.getByRole('button', { name: 'Add draw opportunities' })
    )
    const mode = screen.getByLabelText('Opportunity grant mode')
    expect(
      within(mode).getByRole('option', { name: 'Grant by probability' })
    ).toBeInTheDocument()
  })
  test('check-in exposes daily, additional streak and per-activity make-up rules together', () => {
    show()
    fireEvent.change(screen.getByLabelText('Activity type'), {
      target: { value: 'checkin' },
    })
    expect(screen.getByText('Daily rewards')).toBeVisible()
    expect(
      screen.getByLabelText('Make-ups per user for this activity')
    ).toBeVisible()
    fireEvent.click(
      screen.getByRole('button', { name: 'Add streak milestone' })
    )
    expect(screen.getByLabelText('Consecutive check-in days')).toBeVisible()
    expect(screen.getByText('Milestone rewards')).toBeVisible()
    expect(screen.getAllByLabelText('Gift amount (CNY)')).toHaveLength(2)
  })
})

describe('confirmed activity rules', () => {
  test('cumulative percentages are rejected even when supplied outside the editor', () => {
    const activity = {
      ...initial.activity,
      kind: 'recharge' as const,
      condition: 'cumulative_payments',
      frequency: 'every' as const,
      threshold_cny: '100',
      rewards: [
        {
          kind: 'gift' as const,
          calculation: 'paid_ratio' as const,
          ratio_percent: '10',
          currency: 'CNY',
          valid_seconds: 0,
        },
      ],
    }
    expect(activitySchema.safeParse(activity).success).toBe(false)
  })
  test('CNY thresholds reject gift-like fractional cents but tiny gifts remain valid', () => {
    expect(activitySchema.safeParse(initial.activity).success).toBe(true)
    expect(
      activitySchema.safeParse({
        ...initial.activity,
        kind: 'recharge',
        condition: 'first_payment',
        threshold_cny: '0.001',
      }).success
    ).toBe(false)
  })
  test('switching to invited-friend percentage rewards excludes fixed gifts and draw opportunities', () => {
    const next = changeActivityCondition(
      initial.activity,
      'invitation',
      'friend_ratio',
      'CNY'
    )
    expect(next.rewards).toEqual([
      {
        kind: 'gift',
        calculation: 'cash_ratio',
        ratio_percent: '',
        currency: 'CNY',
        valid_seconds: 0,
      },
    ])
    expect(next.frequency).toBe('every')
  })
  test('make-ups require both limits and duplicate streak milestones are invalid', () => {
    const next = changeActivityCondition(
      initial.activity,
      'checkin',
      'checkin',
      'CNY'
    )
    if (!next.checkin) throw new Error('Expected check-in configuration')
    next.checkin.makeup_days = 3
    expect(activitySchema.safeParse(next).success).toBe(false)
    next.checkin.makeup_limit = 2
    next.checkin.milestones = [
      { days: 7, rewards: initial.activity.rewards },
      { days: 7, rewards: initial.activity.rewards },
    ]
    expect(activitySchema.safeParse(next).success).toBe(false)
    next.checkin.milestones.pop()
    expect(activitySchema.safeParse(next).success).toBe(true)
  })
})

test('an open activity form retains its original amount and conversion revision after background refresh', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  clients.push(client)
  const put = vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true, data: initial } })
  const onClose = vi.fn()
  const view = render(<QueryClientProvider client={client}><ActivityEditor currency='CNY' moneyRevision='rate-a' original={initial} onClose={onClose} /></QueryClientProvider>)
  view.rerender(<QueryClientProvider client={client}><ActivityEditor currency='USD' moneyRevision='rate-b' original={{ ...initial, revision: 8 }} onClose={onClose} /></QueryClientProvider>)
  expect(screen.getByLabelText('Gift amount (CNY)')).toHaveValue('0.000002')
  fireEvent.click(screen.getByRole('button', { name: 'Save draft' }))
  await waitFor(() => expect(onClose).toHaveBeenCalled())
  expect(put.mock.calls[0][1]).toMatchObject({ money_revision: 'rate-a', revision: 2, activity: { rewards: [{ amount: '0.000002', currency: 'CNY' }] } })
})
