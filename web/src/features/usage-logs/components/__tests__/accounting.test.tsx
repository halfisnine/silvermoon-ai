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
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { beforeEach, afterEach, expect, test, vi } from 'vitest'

import type { RequestAccounting } from '@/features/marketing/accounting'
import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { FundingDetails } from '../funding-details'
import { LogCostDisplay } from '../log-cost-display'
import { MonthlyPaidConsumption } from '../monthly-paid-consumption'

const money = (amount: string) => ({ amount, currency: 'CNY' })
const entry: RequestAccounting = {
  id: 10,
  user_id: 1,
  request_id: 'request-1',
  model_name: 'model',
  state: 'settled',
  cost_known: true,
  final_observed: true,
  conversion_known: true,
  qualified_at: 1,
  realtime: false,
  realtime_closed: false,
  actual: money('0.12'),
  total: money('0.12'),
  paid: money('0.04'),
  gift: money('0.08'),
  unsettled: money('0'),
  reserved: money('0'),
  gift_sources: [
    {
      gift_id: 3,
      source_name: 'Registration gift',
      expires_at: 0,
      money: money('0.08'),
    },
  ],
}
let client: QueryClient
beforeEach(async () => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  i18next.addResourceBundle('en', 'translation', en.translation, true, true)
  i18next.addResourceBundle('zh', 'translation', zh.translation, true, true)
  await i18next.changeLanguage('en')
  useAuthStore.getState().auth.setUser({ id: 1, username: 'alice', role: 1 })
})
afterEach(async () => {
  await act(() => i18next.changeLanguage('en'))
  client.clear()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

test('mixed payment remains one cost display with a gift amount and original tiny currency precision', () => {
  render(
    <LogCostDisplay
      quota={999999999}
      other={{
        accounting: {
          ...entry,
          total: money('0.000002'),
          gift: money('0.000001'),
        },
      }}
    />
  )
  expect(screen.getByText('¥0.000002')).toBeVisible()
  expect(screen.getByText('Gift: ¥0.000001')).toBeVisible()
  expect(screen.queryByText('$1,999.999998')).not.toBeInTheDocument()
})
test('zero gift cost does not add an empty gift marker', () => {
  render(
    <LogCostDisplay
      quota={9}
      other={{ accounting: { ...entry, gift: money('0') } }}
    />
  )
  expect(screen.queryByText(/Gift:/)).not.toBeInTheDocument()
  expect(screen.getByText('¥0.12')).toBeVisible()
})
test('unknown historical conversion is explicit rather than rendering current converted quota', () => {
  render(
    <LogCostDisplay
      quota={500000}
      other={{
        accounting: {
          ...entry,
          total: null,
          gift: null,
          conversion_known: false,
        },
      }}
    />
  )
  expect(screen.getByText('Amount unavailable')).toBeVisible()
  expect(screen.queryByText('$1')).not.toBeInTheDocument()
})
test('same detail contains settled paid and gift sources and follows Chinese language changes', async () => {
  render(<FundingDetails accounting={entry} />)
  expect(screen.getByText('Paid amount used')).toBeVisible()
  expect(screen.getByText('Registration gift')).toBeVisible()
  expect(screen.getByText('¥0.04')).toBeVisible()
  expect(screen.getByText('Never expires')).toBeVisible()
  await act(() => i18next.changeLanguage('zh'))
  expect(screen.getByText('消费来源')).toBeVisible()
  expect(screen.getByText('使用付费金额')).toBeVisible()
})
test('pending unknown final result never reports a free or fully settled request', () => {
  render(
    <FundingDetails
      accounting={{
        ...entry,
        state: 'review',
        cost_known: false,
        final_observed: true,
        actual: null,
        unsettled: null,
      }}
    />
  )
  expect(screen.getByText('Needs reconciliation')).toBeVisible()
  expect(screen.getByText('Not yet known')).toBeVisible()
  expect(
    screen.getByText(
      'The final usage result has not been confirmed. This is not a free request.'
    )
  ).toBeVisible()
})
test('known final result distinguishes settled and outstanding cost', () => {
  render(
    <FundingDetails
      accounting={{
        ...entry,
        state: 'review',
        actual: money('0.2'),
        unsettled: money('0.08'),
      }}
    />
  )
  expect(screen.getByText('Actual cost')).toBeVisible()
  expect(screen.getByText('¥0.2')).toBeVisible()
  expect(screen.getByText('Unsettled cost')).toBeVisible()
  expect(
    screen.getByText(
      'Final usage has been received; settlement still needs attention.'
    )
  ).toBeVisible()
})
test('blocked reversal shows the amount awaiting return to its original source', () => {
  render(
    <FundingDetails
      accounting={{
        ...entry,
        state: 'review',
        actual: money('0.1'),
        unsettled: money('0'),
        pending_return: money('0.02'),
      }}
    />
  )
  expect(screen.getByText('Pending source return')).toBeVisible()
  expect(screen.getByText('¥0.02')).toBeVisible()
})

test('monthly dialog loads only when opened and keeps currency groups and missing conversion explicit', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        month: '2026-11',
        amounts: [money('0.000001'), { amount: '-0.02', currency: 'USD' }],
        conversion_unknown_count: 1,
      },
    },
  })
  render(
    <QueryClientProvider client={client}>
      <MonthlyPaidConsumption />
    </QueryClientProvider>
  )
  expect(get).not.toHaveBeenCalled()
  await userEvent.click(
    screen.getByRole('button', { name: 'My monthly paid consumption' })
  )
  const dialog = await screen.findByRole('dialog')
  expect(await within(dialog).findByText('¥0.000001')).toBeVisible()
  expect(within(dialog).getByText('$-0.02')).toBeVisible()
  expect(
    within(dialog).getByText(
      'Some records have no historical conversion and are excluded from these amounts.'
    )
  ).toBeVisible()
  const firstConfig = get.mock.calls[0]?.[1] as
    | { params?: { month?: string } }
    | undefined
  const current = String(firstConfig?.params?.month)
  await userEvent.click(
    within(dialog).getByRole('button', { name: 'Previous month' })
  )
  await screen.findByText('¥0.000001')
  const lastConfig = get.mock.calls.at(-1)?.[1] as
    | { params?: { month?: string } }
    | undefined
  expect(String(lastConfig?.params?.month)).not.toBe(current)
  get.mockResolvedValue({
    data: {
      success: true,
      data: { month: '2026-09', amounts: [], conversion_unknown_count: 1 },
    },
  })
  await userEvent.click(
    within(dialog).getByRole('button', { name: 'Previous month' })
  )
  await waitFor(() =>
    expect(within(dialog).queryByText('¥0.000001')).not.toBeInTheDocument()
  )
  expect(
    within(dialog).queryByText('No settled paid consumption in this month')
  ).not.toBeInTheDocument()
  expect(
    within(dialog).getByText(
      'Some records have no historical conversion and are excluded from these amounts.'
    )
  ).toBeVisible()
})
test('monthly failure displays retry and does not turn a failed query into zero spending', async () => {
  vi.spyOn(api, 'get').mockRejectedValue(new Error('offline'))
  render(
    <QueryClientProvider client={client}>
      <MonthlyPaidConsumption />
    </QueryClientProvider>
  )
  await userEvent.click(
    screen.getByRole('button', { name: 'My monthly paid consumption' })
  )
  expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible()
  expect(
    screen.queryByText('No settled paid consumption in this month')
  ).not.toBeInTheDocument()
})
