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
import { nanoid } from 'nanoid'
import { useRef } from 'react'

// A corrected payload is a new operation; retrying the same payload after a
// lost response keeps its key so successful writes cannot be applied twice.
export function usePoolOperationKey(): (payload: unknown) => string {
  const operation = useRef({ signature: '', key: '' })
  return (payload: unknown) => {
    const signature = JSON.stringify(payload)
    if (signature !== operation.current.signature) {
      operation.current = { signature, key: nanoid() }
    }
    return operation.current.key
  }
}
