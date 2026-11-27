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
import type { BatchSnapshot } from './api'

export type EndpointConfig = Record<string, unknown>

export function endpointCatalog(snapshot: BatchSnapshot): EndpointConfig {
  const catalog: EndpointConfig = { ...snapshot.endpoint_options }
  for (const model of snapshot.metadata) {
    for (const [key, value] of Object.entries(
      parseEndpointConfig(model.endpoints ?? '')
    )) {
      catalog[key] =
        snapshot.endpoint_options?.[key] ?? value ?? catalog[key] ?? null
    }
  }
  return catalog
}

export function parseEndpointConfig(raw: string): EndpointConfig {
  try {
    const parsed: unknown = JSON.parse(raw || '{}')
    if (Array.isArray(parsed)) {
      return Object.fromEntries(
        parsed
          .filter((key): key is string => typeof key === 'string')
          .map((key) => [key, null])
      )
    }
    if (parsed && typeof parsed === 'object') return parsed as EndpointConfig
  } catch {
    // Preserve the original string until the administrator explicitly edits it.
  }
  return {}
}

export function updateEndpointConfig(
  raw: string,
  selected: string[],
  snapshot: BatchSnapshot,
  catalog = endpointCatalog(snapshot)
): string {
  if (!selected.length) return ''
  const current = parseEndpointConfig(raw)
  const next = Object.fromEntries(
    selected.map((key) => [
      key,
      current[key] ?? snapshot.endpoint_options?.[key] ?? catalog[key],
    ])
  )
  // Legacy type arrays can include custom protocol names without known paths.
  // Keep that form rather than inventing paths or dropping selected protocols.
  if (Object.values(next).some((value) => value == null)) {
    return JSON.stringify(selected)
  }
  return JSON.stringify(next)
}
