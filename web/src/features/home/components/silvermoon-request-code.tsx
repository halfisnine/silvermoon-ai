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
import { useEffect, useState } from 'react'

import type { IntegrationCodeToken } from '../lib/highlight-integration'

export function SilvermoonRequestCode(props: { code: string }) {
  const [highlighted, setHighlighted] = useState<{
    code: string
    tokens: IntegrationCodeToken[]
  } | null>(null)

  useEffect(() => {
    let cancelled = false
    import('../lib/highlight-integration')
      .then((module) => module.highlightIntegrationExample(props.code))
      .then((tokens) => {
        if (!cancelled) setHighlighted({ code: props.code, tokens })
      })
      .catch(() => {
        // The request stays readable and copyable if optional coloring cannot load.
        if (!cancelled) setHighlighted(null)
      })
    return () => {
      cancelled = true
    }
  }, [props.code])

  return (
    <code>
      {highlighted?.code === props.code
        ? highlighted.tokens.map((token) => (
            <span key={token.offset} style={{ color: token.color }}>
              {token.content}
            </span>
          ))
        : props.code}
    </code>
  )
}
