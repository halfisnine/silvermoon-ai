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
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useSystemConfigStore } from '@/stores/system-config-store'

import { SilvermoonHome } from '../components/silvermoon-home'

const animationsDescriptor = Object.getOwnPropertyDescriptor(
  Element.prototype,
  'getAnimations'
)
let status: Record<string, unknown>
let client: QueryClient

beforeEach(() => {
  Object.defineProperty(Element.prototype, 'getAnimations', {
    configurable: true,
    value: () => [],
  })
  localStorage.clear()
  useSystemConfigStore.setState(useSystemConfigStore.getInitialState(), true)
  status = {
    server_address: 'https://gateway.example',
    api_info: [
      { url: 'https://route.example/custom/v1', description: 'Primary route' },
    ],
    announcements: [
      { content: 'Scheduled service update', extra: 'More information' },
    ],
  }
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  vi.spyOn(api, 'get').mockImplementation(async () => ({
    data: { success: true, data: status },
  }))
})

afterEach(() => {
  if (animationsDescriptor) {
    Object.defineProperty(
      Element.prototype,
      'getAnimations',
      animationsDescriptor
    )
  } else {
    Reflect.deleteProperty(Element.prototype, 'getAnimations')
  }
  client.clear()
  localStorage.clear()
})

async function renderHome() {
  const router = createRouter({
    routeTree: createRootRoute({ component: SilvermoonHome }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  return render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
}

describe('Silvermoon homepage', () => {
  it('copies the configured endpoint without rewriting its path', async () => {
    const user = userEvent.setup()
    const clipboard = vi
      .spyOn(navigator.clipboard, 'writeText')
      .mockResolvedValue(undefined)
    await renderHome()
    expect(
      await screen.findByText('https://route.example/custom/v1')
    ).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Copy API address' }))
    await waitFor(() =>
      expect(clipboard).toHaveBeenCalledWith('https://route.example/custom/v1')
    )
    expect(
      screen.queryByText('https://gateway.example')
    ).not.toBeInTheDocument()
  })

  it('uses the configured server address when no route is available', async () => {
    status.api_info = []
    await renderHome()
    expect(await screen.findByText('https://gateway.example')).toBeVisible()
  })

  it('does not invent an endpoint when disabled or unconfigured', async () => {
    status.api_info_enabled = false
    await renderHome()
    expect(await screen.findByText('No API routes configured')).toBeVisible()
    expect(
      screen.queryByRole('button', { name: 'Copy API address' })
    ).not.toBeInTheDocument()
  })

  it('opens announcements only on request and closes them with Escape', async () => {
    const user = userEvent.setup()
    await renderHome()
    const trigger = await screen.findByRole('button', {
      name: /Scheduled service update/,
    })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    await user.click(trigger)
    expect(
      await screen.findByRole('dialog', { name: 'Announcement Details' })
    ).toBeVisible()
    expect(screen.getByText('More information')).toBeVisible()
    await user.keyboard('{Escape}')
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
  })

  it('hides disabled announcements and offers a local guide for invalid documentation URLs', async () => {
    const user = userEvent.setup()
    status.announcements_enabled = false
    status.docs_link = 'javascript:alert(1)'
    await renderHome()
    await user.click(
      screen.getByRole('button', { name: 'First visit? Read the setup guide' })
    )
    expect(
      await screen.findByRole('dialog', { name: 'Setup guide' })
    ).toBeVisible()
    expect(
      screen.getByRole('link', { name: 'Create API Key' })
    ).toHaveAttribute('href', '/keys')
    expect(
      screen.queryByText('Scheduled service update')
    ).not.toBeInTheDocument()
  })

  it('uses a configured HTTPS documentation URL', async () => {
    status.docs_link = 'https://docs.example/start'
    await renderHome()
    expect(await screen.findByRole('button', { name: 'Docs' })).toHaveAttribute(
      'href',
      'https://docs.example/start'
    )
  })
})
