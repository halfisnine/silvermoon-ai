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
type OptionsPage<T> = { items: T[]; total: number; page_size: number }

/** Load complete select options while keeping the server's per-request limit. */
export async function loadAllOptions<T extends { id: number }>(
  loadPage: (page: number) => Promise<OptionsPage<T>>
): Promise<T[]> {
  const options = new Map<number, T>()
  for (let page = 1; ; page++) {
    const result = await loadPage(page)
    for (const item of result.items) options.set(item.id, item)
    if (result.items.length === 0 || page * result.page_size >= result.total) {
      return [...options.values()]
    }
  }
}
