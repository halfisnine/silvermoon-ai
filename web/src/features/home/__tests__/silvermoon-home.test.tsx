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
import { execFileSync } from 'node:child_process'

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
import { buildIntegrationExample } from '../lib/integration-example'

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
    await user.click(screen.getByRole('button', { name: 'Copy code' }))
    await waitFor(() =>
      expect(clipboard).toHaveBeenLastCalledWith(
        screen.getByLabelText('cURL request example').textContent
      )
    )
    expect(clipboard.mock.lastCall?.[0]).toContain(
      "curl 'https://route.example/custom/v1/chat/completions'"
    )
    expect(
      screen.queryByText('https://gateway.example')
    ).not.toBeInTheDocument()
  })

  it('offers four protocol examples without terminal decorations and copies the selected request', async () => {
    const user = userEvent.setup()
    const clipboard = vi
      .spyOn(navigator.clipboard, 'writeText')
      .mockResolvedValue(undefined)
    await renderHome()
    await screen.findByText('https://route.example/custom/v1')
    expect(screen.queryByText('● ● ●')).not.toBeInTheDocument()
    const cases = [
      [
        'OpenAI Chat',
        '/custom/v1/chat/completions',
        'Authorization: Bearer',
        '"messages"',
      ],
      [
        'OpenAI Responses',
        '/custom/v1/responses',
        'Authorization: Bearer',
        '"input"',
      ],
      [
        'Anthropic',
        '/custom/v1/messages',
        'anthropic-version: 2023-06-01',
        '"max_tokens": 1024',
      ],
      [
        'Gemini',
        '/custom/v1beta/models/YOUR_MODEL_NAME:generateContent',
        'x-goog-api-key:',
        '"contents"',
      ],
    ]
    for (const [label, path, header, field] of cases) {
      const tab = screen.getByRole('tab', { name: label })
      await user.click(tab)
      expect(tab).toHaveAttribute('aria-selected', 'true')
      const panel = screen.getByRole('tabpanel', { name: label })
      expect(panel).toBeVisible()
      await user.click(screen.getByRole('button', { name: 'Copy code' }))
      await waitFor(() =>
        expect(clipboard).toHaveBeenLastCalledWith(
          screen.getByLabelText('cURL request example').textContent
        )
      )
      expect(clipboard.mock.lastCall?.[0]).toContain(path)
      expect(clipboard.mock.lastCall?.[0]).toContain(header)
      expect(clipboard.mock.lastCall?.[0]).toContain(field)
    }
    await user.click(screen.getByRole('tab', { name: 'OpenAI Chat' }))
    await user.keyboard('{ArrowRight}{Enter}')
    await waitFor(() =>
      expect(
        screen.getByRole('tab', { name: 'OpenAI Responses' })
      ).toHaveAttribute('aria-selected', 'true')
    )
  })

  it('uses the configured server address when no route is available', async () => {
    status.api_info = []
    await renderHome()
    expect(await screen.findByText('https://gateway.example')).toBeVisible()
  })

  it.each(['disabled', 'unconfigured'])(
    'does not invent an endpoint when %s',
    async (state) => {
      if (state === 'disabled') status.api_info_enabled = false
      else {
        status.api_info = []
        status.server_address = ''
      }
      await renderHome()
      expect(await screen.findByText('No API routes configured')).toBeVisible()
      expect(
        screen.queryByRole('button', { name: 'Copy API address' })
      ).not.toBeInTheDocument()
      expect(
        screen.queryByRole('button', { name: 'Copy code' })
      ).not.toBeInTheDocument()
    }
  )

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

describe('Homepage cURL example', () => {
  it.each([
    ['https://gateway.example', 'https://gateway.example/v1/chat/completions'],
    [
      'https://gateway.example/v1/',
      'https://gateway.example/v1/chat/completions',
    ],
    [
      'https://gateway.example/proxy/v1',
      'https://gateway.example/proxy/v1/chat/completions',
    ],
    [
      'https://gateway.example/proxy',
      'https://gateway.example/proxy/chat/completions',
    ],
    [
      'https://gateway.example/v1/chat/completions/',
      'https://gateway.example/v1/chat/completions',
    ],
  ])(
    'builds the request path for %s without duplicating segments',
    (endpoint, expected) => {
      expect(buildIntegrationExample(endpoint).split('\n')[0]).toBe(
        `curl '${expected}' \\`
      )
    }
  )

  it.each([
    [
      'https://gateway.example',
      'responses',
      'https://gateway.example/v1/responses',
    ],
    [
      'https://gateway.example/v1',
      'anthropic',
      'https://gateway.example/v1/messages',
    ],
    [
      'https://gateway.example/v1/',
      'gemini',
      'https://gateway.example/v1beta/models/YOUR_MODEL_NAME:generateContent',
    ],
    [
      'https://gateway.example/proxy/v1',
      'gemini',
      'https://gateway.example/proxy/v1beta/models/YOUR_MODEL_NAME:generateContent',
    ],
    [
      'https://gateway.example/proxy/v1beta',
      'anthropic',
      'https://gateway.example/proxy/v1/messages',
    ],
    [
      'https://gateway.example/proxy/v1/messages',
      'responses',
      'https://gateway.example/proxy/v1/responses',
    ],
    [
      'https://gateway.example/proxy/v1beta/models/old-model:generateContent',
      'openai',
      'https://gateway.example/proxy/v1/chat/completions',
    ],
    [
      'https://gateway.example/proxy/v1beta/models/old-model:streamGenerateContent',
      'gemini',
      'https://gateway.example/proxy/v1beta/models/YOUR_MODEL_NAME:generateContent',
    ],
  ] as const)(
    'switches %s to %s without retaining the old protocol suffix',
    (endpoint, protocol, expected) => {
      expect(buildIntegrationExample(endpoint, protocol).split('\n')[0]).toBe(
        `curl '${expected}' \\`
      )
    }
  )

  it.each([
    [
      'responses',
      '/v1/responses',
      ['Authorization: Bearer test-placeholder-key'],
      { model: 'YOUR_MODEL_NAME', input: 'Hello!' },
    ],
    [
      'anthropic',
      '/v1/messages',
      ['x-api-key: test-placeholder-key', 'anthropic-version: 2023-06-01'],
      {
        model: 'YOUR_MODEL_NAME',
        max_tokens: 1024,
        messages: [{ role: 'user', content: 'Hello!' }],
      },
    ],
    [
      'gemini',
      '/v1beta/models/YOUR_MODEL_NAME:generateContent',
      ['x-goog-api-key: test-placeholder-key'],
      { contents: [{ parts: [{ text: 'Hello!' }] }] },
    ],
  ] as const)(
    'copies a valid %s request with protocol-specific headers and JSON',
    (protocol, path, headers, body) => {
      const example = buildIntegrationExample(
        'https://gateway.example',
        protocol
      )
      const args = execFileSync(
        'sh',
        ['-c', `curl() { printf "%s\\0" "$@"; }\n${example}`],
        {
          env: { SILVERMOON_API_KEY: 'test-placeholder-key' },
          encoding: 'utf8',
        }
      )
        .split('\0')
        .filter(Boolean)
      expect(args[0]).toBe(`https://gateway.example${path}`)
      for (const header of headers) expect(args).toContain(header)
      expect(args).toContain('Content-Type: application/json')
      expect(JSON.parse(args[args.indexOf('-d') + 1])).toEqual(body)
    }
  )

  it.each([
    '',
    'not-a-url',
    'javascript:alert(1)',
    'https://name:secret@gateway.example',
  ])(
    'does not generate a request for invalid or credential-bearing address %s',
    (endpoint) => {
      expect(buildIntegrationExample(endpoint)).toBe('')
    }
  )

  it('keeps shell syntax in a configured URL literal and passes the key and JSON as complete arguments', () => {
    const example = buildIntegrationExample(
      "https://gateway.example/a'b/$(printf%20unsafe)/v1?route=$HOME"
    )
    // Replace curl at the process boundary: no network requests or real credentials.
    const args = execFileSync(
      'sh',
      ['-c', `curl() { printf "%s\\0" "$@"; }\n${example}`],
      {
        env: { SILVERMOON_API_KEY: 'test-placeholder-key' },
        encoding: 'utf8',
      }
    )
      .split('\0')
      .filter(Boolean)
    expect(args).toEqual([
      "https://gateway.example/a'b/$(printf%20unsafe)/v1/chat/completions?route=$HOME",
      '-H',
      'Authorization: Bearer test-placeholder-key',
      '-H',
      'Content-Type: application/json',
      '-d',
      '{\n    "model": "YOUR_MODEL_NAME",\n    "messages": [{"role": "user", "content": "Hello!"}]\n  }',
    ])
  })
})
