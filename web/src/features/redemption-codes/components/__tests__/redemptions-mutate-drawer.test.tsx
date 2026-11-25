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
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  type RenderResult,
} from '@testing-library/react'
import { afterEach, describe, expect, test } from 'vitest'

import type { Redemption } from '../../types'

const i18n = (await import('i18next')).default
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { Toaster, toast } = await import('sonner')
const { api } = await import('@/lib/api')
const { useSystemConfigStore } = await import('@/stores/system-config-store')
const { RedemptionsProvider } = await import('../redemptions-provider')
const { RedemptionsMutateDrawer } = await import('../redemptions-mutate-drawer')

await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Failed to load': 'Failed to load',
        'Loading...': 'Loading...',
        'Save changes': 'Save changes',
        'Something went wrong!': 'Something went wrong!',
      },
    },
  },
})

type ApiMethod = (url: string, data?: unknown) => Promise<{ data: unknown }>
type MockableApi = {
  get: ApiMethod
  post: ApiMethod
  put: ApiMethod
}
type RenderedDrawer = {
  result: RenderResult
}
type CurrencyFixture = {
  quotaDisplayType: 'USD' | 'CNY'
  usdExchangeRate: number
}

const apiClient = api as unknown as MockableApi
const originalGet = apiClient.get
const originalPut = apiClient.put
const originalPost = apiClient.post
const originalConsoleLog = Reflect.get(console, 'log')
let queryClient: QueryClient
let renderedDrawer: RenderedDrawer | null = null

function redemption(id: number, amount = '1.000002'): Redemption {
  return {
    id,
    name: `code-${id}`,
    key: `key-${id}`,
    status: 1,
    money_revision: 'display-v1',
    paid_amount_cny: '10',
    credit_amount: amount,
    credit_currency: 'USD',
    original_credit_amount: amount,
    original_credit_currency: 'USD',
    sales_channel: 'card shop',
    sales_batch: 'launch',
    sales_evidence: '',
    sale_registered: true,
    created_time: 1,
    redeemed_time: 0,
    expired_time: 0,
    redeemed: false,
    used_user_name: '',
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((promiseResolve, promiseReject) => {
    resolve = promiseResolve
    reject = promiseReject
  })
  return { promise, reject, resolve }
}

function drawerTree(currentRow: Redemption | undefined, open = true) {
  return (
    <QueryClientProvider client={queryClient}>
      <I18nextProvider i18n={i18n}>
        <RedemptionsProvider>
          <RedemptionsMutateDrawer
            open={open}
            currentRow={currentRow}
            onOpenChange={() => undefined}
          />
        </RedemptionsProvider>
        <Toaster duration={60_000} />
      </I18nextProvider>
    </QueryClientProvider>
  )
}

async function renderDrawer(
  currentRow: Redemption | undefined,
  currency: CurrencyFixture = {
    quotaDisplayType: 'USD',
    usdExchangeRate: 1,
  }
): Promise<void> {
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  useSystemConfigStore.getState().setConfig({
    currency: {
      displayInCurrency: true,
      quotaDisplayType: currency.quotaDisplayType,
      quotaPerUnit: 500000,
      usdExchangeRate: currency.usdExchangeRate,
      customCurrencySymbol: '¤',
      customCurrencyExchangeRate: 1,
    },
  })

  renderedDrawer = { result: render(drawerTree(currentRow)) }
}

async function rerenderDrawer(
  currentRow: Redemption | undefined,
  open = true
): Promise<void> {
  if (!renderedDrawer) {
    throw new Error('Expected a rendered redemption drawer')
  }
  renderedDrawer.result.rerender(drawerTree(currentRow, open))
}

function getSaveButton(): HTMLButtonElement {
  return screen.getByRole('button', { name: 'Save changes' })
}

function getControlByLabel(labelText: 'Name'): HTMLInputElement
function getControlByLabel(
  labelText: 'Credited paid amount (CNY)'
): HTMLInputElement
function getControlByLabel(
  labelText: 'Credited paid amount (USD)'
): HTMLInputElement
function getControlByLabel(labelText: string): HTMLElement {
  const label = [...document.querySelectorAll<HTMLLabelElement>('label')].find(
    (candidate) => candidate.textContent?.trim() === labelText
  )
  if (!label) {
    throw new Error(`Expected label "${labelText}"`)
  }
  const control =
    label.control ??
    label
      .closest('[data-slot="form-item"]')
      ?.querySelector<HTMLElement>('[data-slot="form-control"], input')
  if (!control) {
    throw new Error(`Expected control for label "${labelText}"`)
  }
  return control
}

function changeInput(input: HTMLInputElement, value: string): void {
  fireEvent.input(input, { target: { value } })
}

function submitForm(): void {
  const form = document.querySelector<HTMLFormElement>('#redemption-form')
  if (!form) {
    throw new Error('Expected redemption form')
  }
  fireEvent.submit(form)
}

async function waitForLoadedForm(): Promise<void> {
  await waitFor(() => expect(getSaveButton()).toBeEnabled())
}

afterEach(() => {
  apiClient.get = originalGet
  apiClient.put = originalPut
  apiClient.post = originalPost
  Reflect.set(console, 'log', originalConsoleLog)
  toast.dismiss()
  localStorage.clear()
  queryClient?.clear()
  renderedDrawer = null
})

describe('redemption drawer', () => {
  test('shows the reported CNY quota without floating-point noise', async () => {
    const original = { ...redemption(1, '200'), credit_currency: 'CNY' }
    apiClient.get = async () => ({ data: { success: true, data: original } })

    await renderDrawer(original, {
      quotaDisplayType: 'CNY',
      usdExchangeRate: 7.2,
    })
    await waitForLoadedForm()

    expect(getControlByLabel('Credited paid amount (CNY)').value).toBe('200')
  })

  test('blocks updates and reports an error when loading rejects', async () => {
    const updates: unknown[] = []
    Reflect.set(console, 'log', () => undefined)
    apiClient.get = async () => {
      throw new Error('network failure')
    }
    apiClient.put = async (_url, data) => {
      updates.push(data)
      return { data: { success: true } }
    }

    await renderDrawer(redemption(1))
    await waitFor(() =>
      expect(document.body).toHaveTextContent('network failure')
    )

    expect(getSaveButton()).toBeDisabled()
    submitForm()
    expect(updates).toEqual([])
  })

  test.each([
    {
      message: 'Redemption code is no longer available',
      expected: 'Redemption code is no longer available',
    },
    { message: undefined, expected: 'Failed to load' },
  ])(
    'blocks updates and reports the server reason or localized fallback: $expected',
    async ({ message, expected }) => {
      apiClient.get = async () => ({
        data: { success: false, message },
      })
      await renderDrawer(redemption(1))
      await waitFor(() => expect(document.body).toHaveTextContent(expected))
      expect(getSaveButton()).toBeDisabled()
    }
  )

  test('keeps the original quota when another field changes', async () => {
    const original = redemption(1)
    const updates: Array<Record<string, unknown>> = []
    apiClient.get = async () => ({ data: { success: true, data: original } })
    apiClient.put = async (_url, data) => {
      expect(data && typeof data === 'object').toBeTruthy()
      updates.push(data as Record<string, unknown>)
      return { data: { success: true, data: original } }
    }

    await renderDrawer(original)
    await waitForLoadedForm()
    expect(getControlByLabel('Credited paid amount (USD)').value).toBe(
      '1.000002'
    )

    changeInput(getControlByLabel('Name'), 'renamed')
    submitForm()
    await waitFor(() => expect(updates).toHaveLength(1))

    expect(updates[0]?.name).toBe('renamed')
    expect(updates[0]?.credit_amount).toBe('1.000002')
    expect(updates[0]).not.toHaveProperty('quota')
    expect(updates[0]?.paid_amount_cny).toBe('10')
    expect(updates[0]?.money_revision).toBe('display-v1')
  })

  test('recalculates quota when the quota field changes', async () => {
    const original = redemption(1)
    const updates: Array<Record<string, unknown>> = []
    apiClient.get = async () => ({ data: { success: true, data: original } })
    apiClient.put = async (_url, data) => {
      expect(data && typeof data === 'object').toBeTruthy()
      updates.push(data as Record<string, unknown>)
      return { data: { success: true, data: original } }
    }

    await renderDrawer(original)
    await waitForLoadedForm()
    changeInput(getControlByLabel('Credited paid amount (USD)'), '2')
    submitForm()
    await waitFor(() => expect(updates).toHaveLength(1))

    expect(updates[0]?.credit_amount).toBe('2')
  })

  test('keeps a non-expiring code unchanged after closing an expiring code', async () => {
    const first = { ...redemption(1), expired_time: 2000000000 }
    const second = redemption(2)
    const detail = deferred<{ data: unknown }>()
    const updates: unknown[] = []
    apiClient.get = (url) =>
      url === '/api/redemption/1'
        ? Promise.resolve({ data: { success: true, data: first } })
        : detail.promise
    apiClient.put = async (_url, data) => {
      updates.push(data)
      return { data: { success: true } }
    }
    await renderDrawer(first)
    await waitForLoadedForm()
    await waitFor(() => expect(screen.getByLabelText('Time')).toBeEnabled())
    fireEvent.click(screen.getAllByRole('button', { name: 'Close' })[0])
    await rerenderDrawer(first, false)
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    await rerenderDrawer(second)
    expect(getSaveButton()).toBeDisabled()
    detail.resolve({ data: { success: true, data: second } })
    await waitForLoadedForm()
    expect(screen.getByRole('button', { name: 'Code validity' })).toBeVisible()
    expect(screen.getByLabelText('Time')).toBeDisabled()
    fireEvent.click(getSaveButton())
    await waitFor(() =>
      expect(updates).toEqual([
        expect.objectContaining({ id: 2, expired_time: 0 }),
      ])
    )
  })

  test.each(['Never', 'Clear'])(
    'submits a non-expiring code when its date is cleared with %s',
    async (action) => {
      const original = { ...redemption(1), expired_time: 2000000000 }
      const updates: unknown[] = []
      apiClient.get = async () => ({ data: { success: true, data: original } })
      apiClient.put = async (_url, data) => {
        updates.push(data)
        return { data: { success: true } }
      }
      await renderDrawer(original)
      await waitForLoadedForm()
      await waitFor(() => expect(screen.getByLabelText('Time')).toBeEnabled())
      fireEvent.click(screen.getByRole('button', { name: action }))
      expect(
        screen.getByRole('button', { name: 'Code validity' })
      ).toBeVisible()
      expect(screen.getByLabelText('Time')).toBeDisabled()
      fireEvent.click(getSaveButton())
      await waitFor(() =>
        expect(updates).toEqual([
          expect.objectContaining({ id: 1, expired_time: 0 }),
        ])
      )
    }
  )

  test('ignores an older response after switching records', async () => {
    const first = redemption(1, '1.000002')
    const second = redemption(2, '2.000002')
    const firstRequest = deferred<{ data: unknown }>()
    const secondRequest = deferred<{ data: unknown }>()
    const requestedUrls: string[] = []
    const updates: Array<Record<string, unknown>> = []
    apiClient.get = (url) => {
      requestedUrls.push(url)
      if (url === '/api/redemption/1') return firstRequest.promise
      if (url === '/api/redemption/2') return secondRequest.promise
      throw new Error(`Unexpected GET ${url}`)
    }
    apiClient.put = async (_url, data) => {
      expect(data && typeof data === 'object').toBeTruthy()
      updates.push(data as Record<string, unknown>)
      return { data: { success: true, data: second } }
    }

    await renderDrawer(first)
    await rerenderDrawer(second)
    await waitFor(() => expect(requestedUrls).toContain('/api/redemption/2'))
    secondRequest.resolve({ data: { success: true, data: second } })
    await waitForLoadedForm()

    firstRequest.resolve({ data: { success: true, data: first } })
    expect(getControlByLabel('Name').value).toBe('code-2')

    changeInput(getControlByLabel('Name'), 'second')
    submitForm()
    await waitFor(() => expect(updates).toHaveLength(1))

    expect(updates[0]?.id).toBe(2)
    expect(updates[0]?.credit_amount).toBe('2.000002')
  })
})

test('correcting an invalid sale price clears validation and permits saving', async () => {
  const original = redemption(1)
  const updates: unknown[] = []
  apiClient.get = async () => ({ data: { success: true, data: original } })
  apiClient.put = async (_url, data) => {
    updates.push(data)
    return { data: { success: true, data: original } }
  }
  await renderDrawer(original)
  await waitForLoadedForm()
  const price = screen.getByLabelText('Actual payment (CNY)')
  fireEvent.change(price, { target: { value: '0.001' } })
  fireEvent.click(getSaveButton())
  await waitFor(() => expect(price).toHaveAttribute('aria-invalid', 'true'))
  expect(updates).toHaveLength(0)
  fireEvent.change(price, { target: { value: '0.01' } })
  fireEvent.click(getSaveButton())
  await waitFor(() => expect(updates).toHaveLength(1))
  expect(updates[0]).toEqual(
    expect.objectContaining({
      paid_amount_cny: '0.01',
      credit_amount: '1.000002',
      money_revision: 'display-v1',
    })
  )
})

test('a rejected save retains the form and permits a corrected retry', async () => {
  const original = redemption(1)
  let attempts = 0
  apiClient.get = async () => ({ data: { success: true, data: original } })
  apiClient.put = async () => ({
    data:
      ++attempts === 1
        ? { success: false, message: 'Sale reference unavailable' }
        : { success: true, data: original },
  })
  await renderDrawer(original)
  await waitForLoadedForm()
  fireEvent.click(getSaveButton())
  await screen.findByRole('alert')
  fireEvent.change(screen.getByLabelText('Sales batch'), {
    target: { value: 'corrected' },
  })
  fireEvent.click(getSaveButton())
  await waitFor(() => expect(attempts).toBe(2))
})

test('failed detail loading exposes Retry and re-enables editing after recovery', async () => {
  let attempts = 0
  apiClient.get = async () => ({
    data:
      ++attempts === 1
        ? { success: false, message: 'Temporary problem' }
        : { success: true, data: redemption(1) },
  })
  await renderDrawer(redemption(1))
  fireEvent.click(await screen.findByRole('button', { name: 'Retry' }))
  await waitForLoadedForm()
  expect(screen.getByLabelText('Sales channel')).toHaveValue('card shop')
})

test('a code redeemed after opening the list cannot have its source edited', async () => {
  apiClient.get = async () => ({
    data: {
      success: true,
      data: { ...redemption(1), redeemed: true, status: 3 },
    },
  })
  await renderDrawer(redemption(1))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Used redemption codes cannot be changed'
  )
  expect(getSaveButton()).toBeDisabled()
  expect(screen.getByLabelText('Sales channel')).toBeDisabled()
})

test('sale details preserve tiny credited amounts and immutable source records', async () => {
  const { RedemptionsSourceDialog } =
    await import('../redemptions-source-dialog')
  const row = {
    ...redemption(1, '0.000002'),
    redeemed: true,
    used_user_name: 'buyer',
    sales_evidence: 'receipt-23',
  }
  apiClient.get = async () => ({ data: { success: true, data: row } })
  queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <I18nextProvider i18n={i18n}>
        <RedemptionsSourceDialog id={1} onClose={() => undefined} />
      </I18nextProvider>
    </QueryClientProvider>
  )
  await screen.findByText('receipt-23')
  expect(screen.getByText('buyer')).toBeVisible()
  expect(
    screen.getByText('Used redemption codes cannot be changed')
  ).toBeVisible()
  expect(document.body).toHaveTextContent('0.000002')
  expect(document.body).not.toHaveTextContent('500000')
  expect(
    screen.queryByRole('button', { name: 'Save changes' })
  ).not.toBeInTheDocument()
})

test('a new paid code keeps its original money snapshot when metadata refreshes', async () => {
  apiClient.get = async () => ({
    data: {
      success: true,
      data: { currency: 'CNY', money_revision: 'rate-old' },
    },
  })
  const writes: unknown[] = []
  apiClient.post = async (_url, data) => {
    writes.push(data)
    return { data: { success: true, data: [] } }
  }
  await renderDrawer(undefined)
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled()
  )
  fireEvent.input(screen.getByLabelText('Name'), {
    target: { value: 'New paid code' },
  })
  fireEvent.input(screen.getByLabelText('Actual payment (CNY)'), {
    target: { value: '100' },
  })
  fireEvent.input(screen.getByLabelText('Credited paid amount (CNY)'), {
    target: { value: '100' },
  })
  fireEvent.input(screen.getByLabelText('Sales channel'), {
    target: { value: 'Shop' },
  })
  fireEvent.input(screen.getByLabelText('Sales batch'), {
    target: { value: 'Batch' },
  })
  await act(async () => {
    queryClient.setQueryData(['marketing-admin-metadata'], {
      currency: 'CNY',
      money_revision: 'rate-new',
    })
  })
  await rerenderDrawer(undefined)
  submitForm()
  await waitFor(() => expect(writes).toHaveLength(1))
  expect(writes[0]).toMatchObject({
    credit_amount: '100',
    money_revision: 'rate-old',
  })
})

test('a paid-code detail refresh cannot overwrite edited values or change their money revision', async () => {
  const original = redemption(1)
  apiClient.get = async () => ({ data: { success: true, data: original } })
  const writes: unknown[] = []
  apiClient.put = async (_url, data) => {
    writes.push(data)
    return { data: { success: true, data: original } }
  }
  await renderDrawer(original)
  await waitForLoadedForm()
  changeInput(getControlByLabel('Name'), 'Unsaved local name')
  changeInput(getControlByLabel('Credited paid amount (USD)'), '12')
  await act(async () => {
    queryClient.setQueryData(['redemption-detail', 1], {
      ...original,
      name: 'Background changed name',
      credit_amount: '24',
      money_revision: 'rate-new',
    })
  })
  await rerenderDrawer(original)
  expect(getControlByLabel('Name')).toHaveValue('Unsaved local name')
  expect(getControlByLabel('Credited paid amount (USD)')).toHaveValue('12')
  submitForm()
  await waitFor(() => expect(writes).toHaveLength(1))
  expect(writes[0]).toMatchObject({
    name: 'Unsaved local name',
    credit_amount: '12',
    money_revision: 'display-v1',
  })
})

test('explicit Refresh reloads the paid-code form after a stale money revision is rejected', async () => {
  const original = redemption(1)
  let latest = original
  const writes: unknown[] = []
  apiClient.get = async () => ({ data: { success: true, data: latest } })
  apiClient.put = async (_url, data) => {
    writes.push(data)
    return {
      data:
        writes.length === 1
          ? { success: false, message: 'Money display settings changed' }
          : { success: true, data: latest },
    }
  }
  await renderDrawer(original)
  await waitForLoadedForm()
  changeInput(getControlByLabel('Name'), 'Unsaved name')
  submitForm()
  await screen.findByRole('alert')
  latest = {
    ...original,
    credit_amount: '2.000004',
    money_revision: 'rate-new',
  }
  fireEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  await waitFor(() =>
    expect(getControlByLabel('Credited paid amount (USD)')).toHaveValue(
      '2.000004'
    )
  )
  expect(getControlByLabel('Name')).toHaveValue(original.name)
  submitForm()
  await waitFor(() => expect(writes).toHaveLength(2))
  expect(writes[1]).toMatchObject({
    credit_amount: '2.000004',
    money_revision: 'rate-new',
  })
})
