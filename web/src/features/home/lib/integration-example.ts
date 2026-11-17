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
export const INTEGRATION_PROTOCOLS = [
  { id: 'openai', label: 'OpenAI Chat' },
  { id: 'responses', label: 'OpenAI Responses' },
  { id: 'anthropic', label: 'Anthropic' },
  { id: 'gemini', label: 'Gemini' },
] as const

export type IntegrationProtocol = (typeof INTEGRATION_PROTOCOLS)[number]['id']

/** Build a shell-safe example using the gateway's public protocol routes. */
export function buildIntegrationExample(
  endpoint: string,
  protocol: IntegrationProtocol = 'openai'
): string {
  let url: URL
  try {
    url = new URL(endpoint)
  } catch {
    return ''
  }
  if (
    !['http:', 'https:'].includes(url.protocol) ||
    url.username ||
    url.password
  ) {
    return ''
  }
  // Accept an origin, API base or known request URL, preserving proxy prefixes.
  let base = url.pathname
    .replace(/\/+$/, '')
    .replace(/\/(?:chat\/completions|responses|messages)$/, '')
    .replace(/\/models\/[^/]+:(?:generateContent|streamGenerateContent)$/, '')
  if (protocol === 'gemini') {
    base = `${base.replace(/\/(?:v1|v1beta)$/, '')}/v1beta`
  } else {
    base = base.replace(/\/v1beta$/, '/v1') || '/v1'
  }

  let path = '/chat/completions'
  let headers = ['  -H "Authorization: Bearer $SILVERMOON_API_KEY" \\']
  let body = [
    '    "model": "YOUR_MODEL_NAME",',
    '    "messages": [{"role": "user", "content": "Hello!"}]',
  ]
  if (protocol === 'responses') {
    path = '/responses'
    body = ['    "model": "YOUR_MODEL_NAME",', '    "input": "Hello!"']
  } else if (protocol === 'anthropic') {
    path = '/messages'
    headers = [
      '  -H "x-api-key: $SILVERMOON_API_KEY" \\',
      '  -H "anthropic-version: 2023-06-01" \\',
    ]
    body = [
      '    "model": "YOUR_MODEL_NAME",',
      '    "max_tokens": 1024,',
      '    "messages": [{"role": "user", "content": "Hello!"}]',
    ]
  } else if (protocol === 'gemini') {
    path = '/models/YOUR_MODEL_NAME:generateContent'
    headers = ['  -H "x-goog-api-key: $SILVERMOON_API_KEY" \\']
    body = ['    "contents": [{"parts": [{"text": "Hello!"}]}]']
  }
  url.pathname = `${base}${path}`
  url.hash = ''
  // Single quotes keep configured $, backticks and shell operators literal.
  const quotedUrl = `'${url.href.replaceAll("'", "'\"'\"'")}'`
  return [
    `curl ${quotedUrl} \\`,
    ...headers,
    '  -H "Content-Type: application/json" \\',
    "  -d '{",
    ...body,
    "  }'",
  ].join('\n')
}
