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
import { afterEach, expect, it, vi } from 'vitest'

import type {
  ModelPricingConfig,
  ModelPricingConversion,
} from '@/features/model-pricing/api'
import { pricingOptions } from '@/features/model-pricing/pricing'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import { usePricingPreferencesStore } from '@/stores/pricing-preferences-store'

import type { BatchSnapshot } from '../api'
import { BatchWorkspace } from '../workspace'

let client: QueryClient | undefined
const snapshot: BatchSnapshot = {
  channel: {
    id: 7,
    name: 'Example',
    type: 1,
    models: 'alpha,beta',
    model_mapping: null,
  },
  rows: [
    { source_name: 'alpha', upstream_name: 'up-alpha', metadata: null },
    { source_name: 'beta', upstream_name: 'up-beta', metadata: null },
  ],
  vendors: [
    {
      id: 1,
      name: 'Saved vendor',
      icon: 'OpenAI',
      status: 1,
      created_time: 0,
      updated_time: 0,
    },
  ],
  metadata: [],
  channels: [],
  version: 'v1',
  endpoint_options: {
    openai: { path: '/v1/chat/completions', method: 'POST' },
    'openai-response': { path: '/v1/responses', method: 'POST' },
    anthropic: { path: '/v1/messages', method: 'POST' },
  },
}
const config: ModelPricingConfig = {
  entries: ['alpha', 'beta'].map((name) => ({
    model_name: name,
    version: 'price-v1',
    configured: { ModelPrice: 0.025 },
    effective: { ModelPrice: 0.025 },
  })),
  options: pricingOptions({}),
  empty_version: 'empty',
}

afterEach(() => {
  client?.clear()
  vi.restoreAllMocks()
  vi.useRealTimers()
  useAuthStore.getState().auth.reset()
  localStorage.clear()
})

async function start(
  options: {
    failLoad?: boolean
    failSave?: boolean
    empty?: boolean
    pricing?: ModelPricingConfig
    snapshot?: BatchSnapshot
    pricingLookup?: (names: string[]) => Promise<ModelPricingConfig>
    conversion?: ModelPricingConversion | Promise<ModelPricingConversion>
  } = {}
) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  usePricingPreferencesStore.setState({ currency: 'USD' })
  vi.spyOn(api, 'get').mockImplementation(async (url, request) => {
    if (url === '/api/channel/7/model_batch') {
      if (options.failLoad) throw new Error('Snapshot unavailable')
      return {
        data: {
          success: true,
          data: options.empty
            ? { ...snapshot, rows: [] }
            : (options.snapshot ?? snapshot),
        },
      }
    }
    if (url === '/api/option/model_pricing') {
      const data = options.pricingLookup
        ? await options.pricingLookup(
            (request?.params as URLSearchParams | undefined)?.getAll('model') ??
              []
          )
        : (options.pricing ?? config)
      return { data: { success: true, data } }
    }
    if (url === '/api/ratio_sync/channels') {
      return { data: { success: true, data: [] } }
    }
    if (url === '/api/pricing') {
      return { data: { success: true, data: [], vendors: [] } }
    }
    throw new Error(`Unexpected GET ${url}`)
  })
  const post = vi.spyOn(api, 'post').mockImplementation(async (url, body) => {
    if (url === '/api/option/model_pricing/convert') {
      const requestPrice = (body as { pricing: { ModelPrice?: number } })
        .pricing.ModelPrice
      return {
        data: {
          success: true,
          data: options.conversion
            ? await options.conversion
            : {
                expression:
                  requestPrice !== undefined
                    ? `tier("request", fixed(${requestPrice}))`
                    : 'tier("base", p * 2 + c * 4 + cr * 0.2)',
                effective: {
                  ModelRatio: 1,
                  CompletionRatio: 2,
                  CacheRatio: 0.1,
                },
              },
        },
      }
    }
    if (url === '/api/option/model_pricing/preview') {
      return {
        data: {
          success: true,
          data: { effective: (body as { pricing: unknown }).pricing },
        },
      }
    }
    if (options.failSave) throw new Error('Concurrent update rejected')
    return { data: { success: true } }
  })
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const root = createRootRoute({
    component: () => <BatchWorkspace channelId={7} />,
  })
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return { post, user: userEvent.setup() }
}

it('opens the advanced editor from the actions column and cancels without changing prices', async () => {
  const { user, post } = await start()
  const input = await screen.findByRole('textbox', {
    name: 'Input price for alpha',
  })
  const original = (input as HTMLInputElement).value
  await user.click(
    screen.getAllByRole('button', { name: 'Advanced settings' })[0]
  )
  const dialog = await screen.findByRole('dialog')
  expect(within(dialog).getByText('Selected models: 1')).toBeVisible()
  expect(within(dialog).getByRole('combobox', { name: 'Pricing' })).toHaveValue(
    'Tiered / advanced'
  )
  await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(input).toHaveValue(original)
  expect(
    post.mock.calls.some(([url]) => url === '/api/channel/7/model_batch')
  ).toBe(false)
})

it('shows incomplete and invalid prices through field state and a tooltip, then clears the warning after correction', async () => {
  const { user } = await start()
  const input = await screen.findByRole('textbox', {
    name: 'Input price for alpha',
  })
  await user.clear(input)
  expect(input).toHaveAttribute('aria-invalid', 'true')
  expect(screen.getByRole('button', { name: 'Review and save' })).toBeDisabled()
  const warning = screen.getByRole('img', { name: 'Invalid pricing value' })
  await user.hover(warning)
  expect(await screen.findByRole('tooltip')).toHaveTextContent(
    'Enter a price of 0 or more. Decimals are supported.'
  )
  await user.unhover(warning)
  await user.type(input, '-1')
  expect(input).toHaveAttribute('aria-invalid', 'true')
  await user.clear(input)
  await user.type(input, '0.0125')
  expect(input).not.toHaveAttribute('aria-invalid', 'true')
  expect(
    screen.queryByRole('img', { name: 'Invalid pricing value' })
  ).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Review and save' })).toBeEnabled()
})

it('selects only filtered models and clears selection without selecting hidden rows', async () => {
  const { user } = await start()
  await screen.findByRole('textbox', { name: 'User model name for alpha' })
  await user.type(
    screen.getByRole('textbox', { name: 'Search models' }),
    'alpha'
  )
  await user.click(
    screen.getByRole('checkbox', { name: 'Select visible models' })
  )
  expect(screen.getByRole('checkbox', { name: 'Select alpha' })).toBeChecked()
  await user.clear(screen.getByRole('textbox', { name: 'Search models' }))
  expect(
    screen.getByRole('checkbox', { name: 'Select beta' })
  ).not.toBeChecked()
  expect(screen.getByRole('button', { name: 'Batch settings' })).toBeEnabled()
  await user.click(screen.getByRole('button', { name: 'Clear selection' }))
  expect(
    screen.getByRole('checkbox', { name: 'Select alpha' })
  ).not.toBeChecked()
  expect(screen.getByRole('button', { name: 'Batch settings' })).toBeDisabled()
})

it('previews decimal multiplication before applying and saves only after final review', async () => {
  const { user, post } = await start()
  await screen.findByRole('textbox', { name: 'User model name for alpha' })
  await user.click(
    screen.getByRole('checkbox', { name: 'Select visible models' })
  )
  await user.click(screen.getByRole('button', { name: 'Batch settings' }))
  const dialog = screen.getByRole('dialog')
  await user.click(within(dialog).getByRole('combobox', { name: 'Pricing' }))
  await user.click(
    await screen.findByRole('option', { name: 'Multiply existing prices' })
  )
  await user.clear(
    within(dialog).getByRole('textbox', { name: 'Price factor' })
  )
  await user.type(
    within(dialog).getByRole('textbox', { name: 'Price factor' }),
    '1.25'
  )
  await user.click(
    within(dialog).getByRole('button', { name: 'Preview changes' })
  )
  await screen.findByRole('button', { name: 'Apply to draft' })
  expect(
    post.mock.calls.filter(([url]) => url === '/api/channel/7/model_batch')
  ).toHaveLength(0)
  expect(within(dialog).getByText('Before')).toBeVisible()
  expect(within(dialog).getByText('After')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Apply to draft' }))
  await user.click(screen.getByRole('button', { name: 'Review and save' }))
  await screen.findByRole('button', { name: 'Save changes' })
  expect(
    post.mock.calls.filter(([url]) => url === '/api/channel/7/model_batch')
  ).toHaveLength(0)
  await user.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/channel/7/model_batch',
      expect.objectContaining({
        expected_version: 'v1',
        changes: expect.arrayContaining([
          expect.objectContaining({
            source_name: 'alpha',
            pricing: { ModelPrice: 0.03125 },
            expected_pricing_version: 'price-v1',
          }),
        ]),
      })
    )
  )
})

it('keeps a renamed model draft visible when save fails and offers explicit reload', async () => {
  const { user } = await start({ failSave: true })
  const name = await screen.findByRole('textbox', {
    name: 'User model name for alpha',
  })
  await user.clear(name)
  await user.type(name, 'alias')
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Review and save' })
    ).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: 'Review and save' }))
  await user.click(await screen.findByRole('button', { name: 'Save changes' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Concurrent update rejected'
  )
  await user.click(screen.getByRole('button', { name: 'Back' }))
  expect(
    screen.getByRole('textbox', { name: 'User model name for alpha' })
  ).toHaveValue('alias')
})

it('keeps price drafts and other rows editable while a renamed price lookup is pending', async () => {
  let resolveLookup!: (value: ModelPricingConfig) => void
  const pending = new Promise<ModelPricingConfig>((resolve) => {
    resolveLookup = resolve
  })
  const lookup = vi.fn((names: string[]) =>
    names.includes('alias') ? pending : Promise.resolve(config)
  )
  const { user, post } = await start({ pricingLookup: lookup })
  const price = await screen.findByRole('textbox', {
    name: 'Input price for alpha',
  })
  await user.clear(price)
  await user.type(price, '0.03125')
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Review and save' })
    ).toBeEnabled()
  )
  const name = screen.getByRole('textbox', {
    name: 'User model name for alpha',
  })
  await user.clear(name)
  await user.type(name, 'alias')
  await waitFor(() =>
    expect(lookup).toHaveBeenCalledWith(['alias', 'alpha', 'beta'])
  )
  expect(price).toBeEnabled()
  expect(
    screen.getByRole('textbox', { name: 'Input price for beta' })
  ).toBeEnabled()
  expect(name).toHaveFocus()
  expect(screen.getByRole('button', { name: 'Review and save' })).toBeDisabled()
  await user.clear(price)
  await user.type(price, '0.0625')
  expect(price).toHaveFocus()
  await act(async () =>
    resolveLookup({
      ...config,
      entries: [
        ...config.entries,
        {
          model_name: 'alias',
          version: 'alias-v9',
          configured: { ModelPrice: 0.9 },
          effective: { ModelPrice: 0.9 },
        },
      ],
    })
  )
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Review and save' })
    ).toBeEnabled()
  )
  expect(price).toHaveValue('0.0625')
  await user.click(screen.getByRole('button', { name: 'Review and save' }))
  await user.click(await screen.findByRole('button', { name: 'Save changes' }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/channel/7/model_batch',
      expect.objectContaining({
        changes: [
          expect.objectContaining({
            model_name: 'alias',
            expected_pricing_version: 'alias-v9',
          }),
        ],
      })
    )
  )
})

it('waits until continuous name input pauses before looking up destination prices', async () => {
  const lookup = vi.fn(async () => config)
  await start({ pricingLookup: lookup })
  const name = await screen.findByRole('textbox', {
    name: 'User model name for alpha',
  })
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  act(() => name.focus())
  fireEvent.change(name, { target: { value: 'n' } })
  fireEvent.change(name, { target: { value: 'new-' } })
  fireEvent.change(name, { target: { value: 'new-model' } })
  expect(name).toHaveValue('new-model')
  expect(name).toHaveFocus()
  expect(lookup).toHaveBeenCalledTimes(1)
  await act(async () => vi.advanceTimersByTimeAsync(300))
  expect(lookup).toHaveBeenCalledTimes(2)
  expect(lookup).toHaveBeenLastCalledWith(['alpha', 'beta', 'new-model'])
})

it('preserves edited vendor and decimal pricing across renames and saves using the target price version', async () => {
  const { user, post } = await start({
    pricing: {
      ...config,
      entries: [
        ...config.entries,
        {
          model_name: 'alias',
          version: 'alias-v8',
          configured: { ModelPrice: 0.9 },
          effective: { ModelPrice: 0.9 },
        },
      ],
    },
  })
  const amount = await screen.findByRole('textbox', {
    name: 'Input price for alpha',
  })
  await user.clear(amount)
  await user.type(amount, '0.03125')
  const vendor = screen.getByRole('combobox', { name: 'Vendor for alpha' })
  await user.click(vendor)
  await user.click(await screen.findByRole('option', { name: 'Saved vendor' }))
  const name = screen.getByRole('textbox', {
    name: 'User model name for alpha',
  })
  await user.clear(name)
  await user.type(name, 'alias')
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Review and save' })
    ).toBeEnabled()
  )
  expect(amount).toHaveValue('0.03125')
  expect(vendor).toHaveValue('Saved vendor')
  await user.click(screen.getByRole('button', { name: 'Review and save' }))
  await user.click(await screen.findByRole('button', { name: 'Save changes' }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/channel/7/model_batch',
      expect.objectContaining({
        changes: [
          expect.objectContaining({
            source_name: 'alpha',
            model_name: 'alias',
            vendor_id: 1,
            expected_pricing_version: 'alias-v8',
            pricing: expect.objectContaining({
              'billing_setting.billing_expr': 'tier("request", fixed(0.03125))',
            }),
          }),
        ],
      })
    )
  )
})

it('keeps incomplete cache drafts and validation when renaming an edited token model', async () => {
  const expression = {
    'billing_setting.billing_mode': 'tiered_expr',
    'billing_setting.billing_expr':
      'tier("base", p * 2 + c * 10 + cr * 0.2 + cc * 2.5 + cc12h * 5)',
  }
  const { user, post } = await start({
    pricing: {
      ...config,
      entries: config.entries.map((entry) => ({
        ...entry,
        configured: expression,
        effective: expression,
      })),
    },
  })
  const read = await screen.findByRole('textbox', {
    name: 'Cache read price for alpha',
  })
  await user.clear(read)
  const name = screen.getByRole('textbox', {
    name: 'User model name for alpha',
  })
  await user.clear(name)
  await user.type(name, 'new-name')
  expect(read).toHaveValue('')
  expect(read).toHaveAttribute('aria-invalid', 'true')
  expect(
    screen.getByRole('textbox', { name: 'Cache write price for alpha' })
  ).toHaveValue('2.5')
  expect(screen.getByRole('button', { name: 'Review and save' })).toBeDisabled()
  await user.type(read, '0.0125')
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Review and save' })
    ).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: 'Review and save' }))
  await screen.findByRole('button', { name: 'Save changes' })
  expect(post).toHaveBeenCalledWith(
    '/api/option/model_pricing/preview',
    expect.objectContaining({
      model_name: 'new-name',
      pricing: expect.objectContaining({
        'billing_setting.billing_expr': expect.stringContaining('cc12h * 5'),
      }),
    })
  )
})

it('preserves an in-flight legacy price edit when renaming before conversion completes', async () => {
  let resolveConversion!: (result: ModelPricingConversion) => void
  const conversion = new Promise<ModelPricingConversion>((resolve) => {
    resolveConversion = resolve
  })
  const { user, post } = await start({ conversion })
  const amount = await screen.findByRole('textbox', {
    name: 'Input price for alpha',
  })
  await user.clear(amount)
  await user.type(amount, '0.0125')
  const name = screen.getByRole('textbox', {
    name: 'User model name for alpha',
  })
  await user.clear(name)
  await user.type(name, 'new-name')
  expect(amount).toHaveValue('0.0125')
  await act(async () =>
    resolveConversion({ expression: 'tier("request", fixed(0.025))' })
  )
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Review and save' })
    ).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: 'Review and save' }))
  await user.click(await screen.findByRole('button', { name: 'Save changes' }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/channel/7/model_batch',
      expect.objectContaining({
        changes: [
          expect.objectContaining({
            model_name: 'new-name',
            expected_pricing_version: 'empty',
            pricing: expect.objectContaining({
              'billing_setting.billing_expr': 'tier("request", fixed(0.0125))',
            }),
          }),
        ],
      })
    )
  )
})

it('disables saving duplicate customer names and preserves the edited input', async () => {
  const { user } = await start()
  const name = await screen.findByRole('textbox', {
    name: 'User model name for alpha',
  })
  await user.clear(name)
  await user.type(name, 'beta')
  expect(screen.getByRole('alert')).toHaveTextContent(
    'Customer model name is already used by this channel'
  )
  expect(name).toHaveValue('beta')
  expect(screen.getByRole('button', { name: 'Review and save' })).toBeDisabled()
})

it('shows an empty state and disables batch actions for a channel without models', async () => {
  await start({ empty: true })
  expect(await screen.findByText('No models found')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Batch settings' })).toBeDisabled()
})

it('shows a retry action when channel data cannot be loaded', async () => {
  await start({ failLoad: true })
  expect(await screen.findByText('Snapshot unavailable')).toBeVisible()
  expect(screen.getByRole('button', { name: /retry/i })).toBeEnabled()
})

it('edits decimal prices inline without opening the editor, with final preview before save', async () => {
  const { user, post } = await start()
  const amount = await screen.findByRole('textbox', {
    name: 'Input price for alpha',
  })
  await user.clear(amount)
  expect(screen.getByRole('button', { name: 'Review and save' })).toBeDisabled()
  await user.type(amount, '0.03125')
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(amount).toHaveValue('0.03125')
  await user.click(screen.getByRole('button', { name: 'Review and save' }))
  await screen.findByRole('button', { name: 'Save changes' })
  expect(post).toHaveBeenCalledWith(
    '/api/option/model_pricing/preview',
    expect.objectContaining({
      model_name: 'alpha',
      pricing: expect.objectContaining({
        ModelPrice: 0.025,
        'billing_setting.billing_mode': 'tiered_expr',
        'billing_setting.billing_expr': 'tier("request", fixed(0.03125))',
      }),
    })
  )
  expect(
    post.mock.calls.filter(([url]) => url === '/api/channel/7/model_batch')
  ).toHaveLength(0)
})

it('provides batch vendors and uniform decimal request pricing for every selected model', async () => {
  const { user, post } = await start()
  await screen.findByRole('textbox', { name: 'Input price for alpha' })
  await user.click(
    screen.getByRole('checkbox', { name: 'Select visible models' })
  )
  await user.click(screen.getByRole('button', { name: 'Batch settings' }))
  const dialog = screen.getByRole('dialog')
  await user.click(within(dialog).getByRole('combobox', { name: 'Vendor' }))
  await user.click(await screen.findByRole('option', { name: 'Saved vendor' }))
  await user.click(within(dialog).getByRole('combobox', { name: 'Pricing' }))
  await user.click(
    await screen.findByRole('option', { name: 'Set uniform request price' })
  )
  await user.type(
    within(dialog).getByRole('textbox', {
      name: 'Input price for Selected models',
    }),
    '0.0125'
  )
  expect(within(dialog).queryByText('Fixed price')).not.toBeInTheDocument()
  await user.click(
    within(dialog).getByRole('button', { name: 'Preview changes' })
  )
  await screen.findByRole('button', { name: 'Apply to draft' })
  for (const name of ['alpha', 'beta']) {
    expect(post).toHaveBeenCalledWith(
      '/api/option/model_pricing/preview',
      expect.objectContaining({
        model_name: name,
        pricing: expect.objectContaining({
          'billing_setting.billing_expr': 'tier("request", fixed(0.0125))',
        }),
      })
    )
  }
  await user.click(screen.getByRole('button', { name: 'Apply to draft' }))
  expect(
    screen.getByRole('textbox', { name: 'Input price for alpha' })
  ).toHaveValue('0.0125')
  expect(
    screen.getByRole('textbox', { name: 'Input price for beta' })
  ).toHaveValue('0.0125')
  const name = screen.getByRole('textbox', {
    name: 'User model name for alpha',
  })
  await user.clear(name)
  await user.type(name, 'batch-alias')
  expect(
    screen.getByRole('textbox', { name: 'Input price for alpha' })
  ).toHaveValue('0.0125')
  expect(
    screen.getByRole('combobox', { name: 'Vendor for alpha' })
  ).toHaveValue('Saved vendor')
})

it('keeps a row in the incomplete filter while its newly completed price is still being edited', async () => {
  const { user } = await start()
  await screen.findByRole('textbox', { name: 'Input price for alpha' })
  await user.click(screen.getByRole('combobox', { name: 'Filter models' }))
  await user.click(await screen.findByRole('option', { name: /^Incomplete$/ }))
  await user.click(screen.getByRole('combobox', { name: 'Vendor for alpha' }))
  await user.click(await screen.findByRole('option', { name: 'Saved vendor' }))
  const amount = screen.getByRole('textbox', { name: 'Input price for alpha' })
  await user.clear(amount)
  await user.type(amount, '0.0125')
  expect(amount).toHaveFocus()
  expect(amount).toHaveValue('0.0125')
  await user.click(screen.getByRole('textbox', { name: 'Search models' }))
  expect(
    screen.queryByRole('textbox', { name: 'Input price for alpha' })
  ).not.toBeInTheDocument()
})

it('sets independent decimal cache prices for every selected model before applying the batch', async () => {
  const { user, post } = await start()
  await screen.findByRole('textbox', { name: 'Input price for alpha' })
  await user.click(
    screen.getByRole('checkbox', { name: 'Select visible models' })
  )
  await user.click(screen.getByRole('button', { name: 'Batch settings' }))
  const dialog = screen.getByRole('dialog')
  await user.click(within(dialog).getByRole('combobox', { name: 'Pricing' }))
  await user.click(
    await screen.findByRole('option', { name: 'Set uniform token prices' })
  )
  await user.type(
    within(dialog).getByRole('textbox', {
      name: 'Input price for Selected models',
    }),
    '2'
  )
  await user.type(
    within(dialog).getByRole('textbox', {
      name: 'Output price for Selected models',
    }),
    '10'
  )
  expect(
    within(dialog).getByRole('textbox', {
      name: 'Cache read price for Selected models',
    })
  ).toBeDisabled()
  await user.click(
    within(dialog).getByRole('switch', {
      name: 'Cache read price for Selected models',
    })
  )
  await user.type(
    within(dialog).getByRole('textbox', {
      name: 'Cache read price for Selected models',
    }),
    '0'
  )
  await user.click(
    within(dialog).getByRole('switch', {
      name: 'Cache write price for Selected models',
    })
  )
  await user.type(
    within(dialog).getByRole('textbox', {
      name: 'Cache write price for Selected models',
    }),
    '0.03125'
  )
  await user.click(
    within(dialog).getByRole('button', { name: 'Preview changes' })
  )
  await screen.findByRole('button', { name: 'Apply to draft' })
  for (const model of ['alpha', 'beta']) {
    const call = post.mock.calls.find(
      ([url, body]) =>
        url === '/api/option/model_pricing/preview' &&
        (body as { model_name: string }).model_name === model
    )
    expect(call?.[1]).toMatchObject({
      pricing: {
        'billing_setting.billing_expr': expect.stringContaining('cc * 0.03125'),
      },
    })
    expect(call?.[1]).toMatchObject({
      pricing: {
        'billing_setting.billing_expr': expect.stringContaining('cr * 0'),
      },
    })
  }
  expect(
    post.mock.calls.some(([url]) => url === '/api/channel/7/model_batch')
  ).toBe(false)
  await user.click(screen.getByRole('button', { name: 'Apply to draft' }))
  const read = screen.getByRole('switch', {
    name: 'Cache read price for alpha',
  })
  const write = screen.getByRole('textbox', {
    name: 'Cache write price for alpha',
  })
  expect(read).toBeChecked()
  expect(write).toHaveValue('0.03125')
  await user.clear(write)
  await user.type(write, '0.0125')
  expect(write).toHaveValue('0.0125')
  await user.click(read)
  expect(
    screen.getByRole('textbox', { name: 'Cache read price for alpha' })
  ).toBeDisabled()
  expect(
    screen.getByRole('switch', { name: 'Cache write price for alpha' })
  ).toBeChecked()
})

it('converts legacy input edits to expressions while preserving other prices and request rules', async () => {
  const legacy = {
    ModelRatio: 1,
    CompletionRatio: 2,
    CacheRatio: 0.1,
    CreateCacheRatio: 1.25,
    ImageRatio: 2,
  }
  const { user, post } = await start({
    pricing: {
      ...config,
      entries: config.entries.map((entry) => ({
        ...entry,
        configured: legacy,
        effective: legacy,
      })),
    },
    conversion: {
      expression:
        '(tier("base", p * 2 + c * 4 + cr * 0.2 + cc * 2.5 + cc12h * 5 + img * 4)) * (param("fast") == true ? 2 : 1)',
    },
  })
  const input = await screen.findByRole('textbox', {
    name: 'Input price for alpha',
  })
  await user.clear(input)
  await user.type(input, '0.125')
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Review and save' })
    ).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: 'Review and save' }))
  await screen.findByRole('button', { name: 'Save changes' })
  expect(post).toHaveBeenCalledWith('/api/option/model_pricing/convert', {
    model_name: 'alpha',
    pricing: legacy,
  })
  const preview = post.mock.calls.find(
    ([url]) => url === '/api/option/model_pricing/preview'
  )?.[1] as { model_name: string; pricing: Record<string, unknown> }
  expect(preview.model_name).toBe('alpha')
  expect(preview.pricing['billing_setting.billing_mode']).toBe('tiered_expr')
  const expr = String(preview.pricing['billing_setting.billing_expr'])
  for (const term of [
    'p * 0.125',
    'c * 4',
    'cr * 0.2',
    'cc * 2.5',
    'cc12h * 5',
    'img * 4',
    'param("fast")',
  ]) {
    expect(expr).toContain(term)
  }
  expect(preview.pricing.ModelRatio).toBe(1)
  expect(
    post.mock.calls.some(
      ([url, body]) =>
        url === '/api/option/model_pricing/convert' &&
        (body as { model_name: string }).model_name === 'beta'
    )
  ).toBe(false)
  expect(
    post.mock.calls.some(([url]) => url === '/api/channel/7/model_batch')
  ).toBe(false)
})

it('blocks unsafe legacy conversions and keeps the entered draft for advanced editing', async () => {
  const { user, post } = await start({
    conversion: { unsupported_reason: 'Legacy rules need advanced editing' },
  })
  const amount = await screen.findByRole('textbox', {
    name: 'Input price for alpha',
  })
  await user.clear(amount)
  await user.type(amount, '0.0125')
  await user.hover(screen.getByRole('img', { name: 'Invalid pricing value' }))
  expect(await screen.findByRole('tooltip')).toHaveTextContent(
    'Legacy rules need advanced editing'
  )
  expect(amount).toHaveValue('0.0125')
  expect(screen.getByRole('button', { name: 'Review and save' })).toBeDisabled()
  expect(
    post.mock.calls.some(([url]) => url === '/api/channel/7/model_batch')
  ).toBe(false)
})

it('does not restore a discarded price draft when an earlier conversion finishes', async () => {
  let resolveConversion!: (result: ModelPricingConversion) => void
  const conversion = new Promise<ModelPricingConversion>((resolve) => {
    resolveConversion = resolve
  })
  const { user } = await start({ conversion })
  const amount = await screen.findByRole('textbox', {
    name: 'Input price for alpha',
  })
  await user.clear(amount)
  await user.type(amount, '0.0125')
  expect(screen.getByRole('button', { name: 'Review and save' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: 'Discard changes' }))
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Discard changes',
    })
  )
  await act(async () =>
    resolveConversion({ expression: 'tier("request", fixed(0.025))' })
  )
  await waitFor(() => expect(amount).toHaveValue('0.025'))
  expect(screen.getByRole('button', { name: 'Review and save' })).toBeDisabled()
})

it('keeps the latest decimal input when legacy conversion completes after several edits', async () => {
  let resolveConversion!: (result: ModelPricingConversion) => void
  const conversion = new Promise<ModelPricingConversion>((resolve) => {
    resolveConversion = resolve
  })
  const { user, post } = await start({ conversion })
  const amount = await screen.findByRole('textbox', {
    name: 'Input price for alpha',
  })
  await user.clear(amount)
  await user.type(amount, '0.0125')
  expect(amount).toHaveValue('0.0125')
  expect(screen.getByRole('button', { name: 'Review and save' })).toBeDisabled()
  await act(async () =>
    resolveConversion({ expression: 'tier("request", fixed(0.025))' })
  )
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Review and save' })
    ).toBeEnabled()
  )
  expect(amount).toHaveValue('0.0125')
  await user.click(screen.getByRole('button', { name: 'Review and save' }))
  await screen.findByRole('button', { name: 'Save changes' })
  expect(post).toHaveBeenCalledWith(
    '/api/option/model_pricing/preview',
    expect.objectContaining({
      pricing: expect.objectContaining({
        'billing_setting.billing_mode': 'tiered_expr',
        'billing_setting.billing_expr': 'tier("request", fixed(0.0125))',
      }),
    })
  )
})

it('reads legacy cache conversion without saving and only converts the edited model draft', async () => {
  const pricing: ModelPricingConfig = {
    ...config,
    entries: config.entries.map((entry) => ({
      ...entry,
      configured: { ModelRatio: 1, CacheRatio: 0.1 },
      effective: { ModelRatio: 1, CompletionRatio: 2, CacheRatio: 0.1 },
    })),
  }
  const { user, post } = await start({ pricing })
  await user.click(
    await screen.findByRole('textbox', { name: 'Input price for alpha' })
  )
  const amount = await screen.findByRole('textbox', {
    name: 'Cache read price for alpha',
  })
  await waitFor(() => expect(amount).toBeEnabled())
  expect(post).toHaveBeenCalledWith(
    '/api/option/model_pricing/convert',
    expect.objectContaining({ model_name: 'alpha' })
  )
  expect(screen.getByRole('button', { name: 'Review and save' })).toBeDisabled()
  await user.clear(amount)
  await user.type(amount, '0.0125')
  expect(amount).toHaveValue('0.0125')
  await user.keyboard('{Escape}')
  await user.click(screen.getByRole('button', { name: 'Review and save' }))
  await screen.findByRole('button', { name: 'Save changes' })
  expect(post).toHaveBeenCalledWith(
    '/api/option/model_pricing/preview',
    expect.objectContaining({
      model_name: 'alpha',
      pricing: expect.objectContaining({
        'billing_setting.billing_expr': expect.stringContaining('cr * 0.0125'),
      }),
    })
  )
  expect(
    post.mock.calls.some(([url]) => url === '/api/channel/7/model_batch')
  ).toBe(false)
})

it('shows cache switches and prices directly in model rows and removes the reference-price controls', async () => {
  const pricing: ModelPricingConfig = {
    ...config,
    entries: config.entries.map((entry) => ({
      ...entry,
      configured: {},
      effective: {
        CompletionRatio: 1,
        CacheRatio: 1,
        CreateCacheRatio: 1.25,
        ImageRatio: 1,
      },
    })),
  }
  const { user } = await start({ pricing })
  const read = await screen.findByRole('switch', {
    name: 'Cache read price for alpha',
  })
  const write = screen.getByRole('switch', {
    name: 'Cache write price for alpha',
  })
  expect(read).toBeVisible()
  expect(write).toBeVisible()
  expect(
    screen.getAllByRole('columnheader').map((cell) => cell.textContent)
  ).toEqual([
    '',
    'User model name',
    'Vendor',
    'Customer pricing (USD)',
    'Upstream model',
    'Square display',
    'Endpoint selection',
    'Status',
    'Actions',
  ])
  const row = screen
    .getByRole('textbox', { name: 'User model name for alpha' })
    .closest('tr')
  if (!row) throw new Error('The user model name must belong to a model row')
  const cells = within(row).getAllByRole('cell')
  expect(
    within(cells[2]).getByRole('combobox', { name: 'Vendor for alpha' })
  ).toBeVisible()
  expect(cells[4]).toHaveTextContent('alpha')
  expect(
    within(cells[3]).queryByRole('button', { name: 'Advanced settings' })
  ).not.toBeInTheDocument()
  expect(
    within(cells[8]).getByRole('button', { name: 'Advanced settings' })
  ).toBeEnabled()
  expect(within(cells[3]).getByText('Read cache')).toBeVisible()
  expect(within(cells[3]).getByText('Write cache')).toBeVisible()
  expect(within(cells[3]).getAllByText('/ 1M')).toHaveLength(2)
  expect(
    screen.queryByRole('columnheader', { name: 'Upstream reference price' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: /Reference price source/ })
  ).not.toBeInTheDocument()
  expect(screen.queryByText('Missing reference price')).not.toBeInTheDocument()
  expect(
    vi
      .mocked(api.get)
      .mock.calls.some(([url]) => String(url).startsWith('/api/ratio_sync'))
  ).toBe(false)
  await user.type(
    screen.getByRole('textbox', { name: 'Input price for alpha' }),
    '2'
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Output price for alpha' }),
    '10'
  )
  await user.click(read)
  const amount = screen.getByRole('textbox', {
    name: 'Cache read price for alpha',
  })
  await user.type(amount, '0.0125')
  expect(amount).toHaveValue('0.0125')
  expect(write).not.toBeChecked()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Review and save' })).toBeEnabled()
  await user.click(screen.getByRole('button', { name: 'Review and save' }))
  await screen.findByRole('button', { name: 'Save changes' })
  expect(vi.mocked(api.post)).toHaveBeenCalledWith(
    '/api/option/model_pricing/preview',
    expect.objectContaining({
      model_name: 'alpha',
      pricing: expect.objectContaining({
        'billing_setting.billing_expr': expect.stringContaining('cr * 0.0125'),
      }),
    })
  )
})

it('edits square display and searchable endpoints inline and retains both while renaming', async () => {
  const { user, post } = await start()
  await screen.findByRole('textbox', { name: 'User model name for alpha' })
  expect(
    screen.queryByRole('button', { name: 'Batch pricing' })
  ).not.toBeInTheDocument()
  const square = screen.getByRole('switch', {
    name: 'Square display for alpha',
  })
  expect(square).not.toBeChecked()
  await user.click(square)
  const endpoints = screen.getByRole('combobox', {
    name: 'Endpoints for alpha',
  })
  await user.type(endpoints, '/responses')
  expect(
    await screen.findByRole('option', { name: 'openai-response' })
  ).toBeVisible()
  expect(
    screen.queryByRole('option', { name: 'anthropic' })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('option', { name: 'openai-response' }))
  await user.click(endpoints)
  await user.click(await screen.findByRole('option', { name: 'openai' }))
  if (endpoints.getAttribute('aria-expanded') === 'true') {
    await user.keyboard('{Escape}')
  }
  const name = screen.getByRole('textbox', {
    name: 'User model name for alpha',
  })
  await user.clear(name)
  await user.type(name, 'customer-alpha')
  expect(square).toBeChecked()
  const row = name.closest('tr')
  if (!row) throw new Error('Missing model row')
  expect(within(row).getByText('+1')).toBeVisible()
  expect(within(row).getByText('openai-response')).toBeVisible()
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Review and save' })
    ).toBeEnabled()
  )
  await user.click(screen.getByRole('button', { name: 'Review and save' }))
  const dialog = await screen.findByRole('dialog')
  expect(
    within(dialog).getByText(/Square display.*Hidden.*Shown/)
  ).toBeVisible()
  expect(
    post.mock.calls.filter(([url]) => url === '/api/channel/7/model_batch')
  ).toHaveLength(0)
  await user.click(within(dialog).getByRole('button', { name: 'Save changes' }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      '/api/channel/7/model_batch',
      expect.objectContaining({
        changes: [
          expect.objectContaining({
            model_name: 'customer-alpha',
            status: 1,
            endpoints: JSON.stringify({
              'openai-response': snapshot.endpoint_options?.['openai-response'],
              openai: snapshot.endpoint_options?.openai,
            }),
          }),
        ],
      })
    )
  )
})

it('previews batch visibility and endpoint changes and preserves each model custom path', async () => {
  const metadata = ['alpha', 'beta'].map((model_name, index) => ({
    id: index + 1,
    model_name,
    name_rule: 0 as const,
    status: index === 0 ? 1 : 0,
    sync_official: 0,
    created_time: 0,
    updated_time: 0,
    endpoints: JSON.stringify({
      openai: {
        path: `/${model_name}/chat`,
        method: 'POST',
        custom: model_name,
      },
    }),
  }))
  const custom = {
    ...snapshot,
    metadata,
    rows: snapshot.rows.map((row, index) => ({
      ...row,
      metadata: metadata[index],
    })),
  }
  const { user, post } = await start({ snapshot: custom })
  await screen.findByRole('textbox', { name: 'User model name for alpha' })
  await user.click(
    screen.getByRole('checkbox', { name: 'Select visible models' })
  )
  await user.click(screen.getByRole('button', { name: 'Batch settings' }))
  let dialog = screen.getByRole('dialog')
  expect(
    within(dialog).getByRole('combobox', { name: 'Square display' })
  ).toHaveValue('Keep existing visibility')
  expect(
    within(dialog).getByRole('combobox', { name: 'Endpoint selection' })
  ).toHaveValue('Keep existing endpoints')
  await user.click(
    within(dialog).getByRole('combobox', { name: 'Square display' })
  )
  await user.click(await screen.findByRole('option', { name: 'Shown' }))
  await user.click(
    within(dialog).getByRole('combobox', { name: 'Endpoint selection' })
  )
  await user.click(await screen.findByRole('option', { name: 'Set endpoints' }))
  const endpointInput = within(dialog).getByRole('combobox', {
    name: 'Select endpoints',
  })
  await user.click(endpointInput)
  await user.click(await screen.findByRole('option', { name: 'openai' }))
  if (endpointInput.getAttribute('aria-expanded') === 'true') {
    await user.keyboard('{Escape}')
  }
  await user.click(
    within(dialog).getByRole('button', { name: 'Preview changes' })
  )
  await screen.findByRole('button', { name: 'Apply to draft' })
  expect(within(dialog).getByText('Hidden → Shown')).toBeVisible()
  expect(
    post.mock.calls.filter(([url]) => url === '/api/channel/7/model_batch')
  ).toHaveLength(0)
  await user.click(
    within(dialog).getByRole('button', { name: 'Apply to draft' })
  )
  expect(
    screen.getByRole('switch', { name: 'Square display for beta' })
  ).toBeChecked()
  await user.click(screen.getByRole('button', { name: 'Review and save' }))
  dialog = await screen.findByRole('dialog')
  await user.click(within(dialog).getByRole('button', { name: 'Save changes' }))
  await waitFor(() =>
    expect(
      post.mock.calls.filter(([url]) => url === '/api/channel/7/model_batch')
    ).toHaveLength(1)
  )
  const body = post.mock.calls.find(
    ([url]) => url === '/api/channel/7/model_batch'
  )?.[1] as {
    changes: {
      model_name: string
      status: number
      endpoints: string
      pricing?: unknown
    }[]
  }
  expect(body.changes).toHaveLength(2)
  body.changes.forEach((change, index) => {
    expect(change.status).toBe(1)
    expect(JSON.parse(change.endpoints)).toEqual(
      JSON.parse(metadata[index].endpoints)
    )
    expect(change).not.toHaveProperty('pricing')
  })
})
