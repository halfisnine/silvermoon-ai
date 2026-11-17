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
/** Build a shell-safe OpenAI-compatible example from the public site address. */
export function buildIntegrationExample(endpoint: string): string {
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
  const path = url.pathname.replace(/\/+$/, '')
  url.pathname = path.endsWith('/chat/completions')
    ? path
    : `${path || '/v1'}/chat/completions`
  url.hash = ''
  // Single quotes keep configured $, backticks and shell operators literal.
  const quotedUrl = `'${url.href.replaceAll("'", "'\"'\"'")}'`
  return [
    `curl ${quotedUrl} \\`,
    '  -H "Authorization: Bearer $OPENAI_API_KEY" \\',
    '  -H "Content-Type: application/json" \\',
    "  -d '{",
    '    "model": "YOUR_MODEL_NAME",',
    '    "messages": [{"role": "user", "content": "Hello!"}]',
    "  }'",
  ].join('\n')
}
