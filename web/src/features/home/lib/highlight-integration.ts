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
import { createHighlighterCore } from 'shiki/core'
import { createJavaScriptRegexEngine } from 'shiki/engine/javascript'
import json from 'shiki/langs/json.mjs'
import shellscript from 'shiki/langs/shellscript.mjs'

export interface IntegrationCodeToken {
  content: string
  color: string
  offset: number
}

let highlighter: ReturnType<typeof createHighlighterCore> | undefined

export async function highlightIntegrationExample(
  code: string
): Promise<IntegrationCodeToken[]> {
  highlighter ??= createHighlighterCore({
    engine: createJavaScriptRegexEngine(),
    langs: [shellscript, json],
    themes: [
      {
        name: 'silvermoon-code',
        type: 'dark',
        colors: {
          'editor.background': '#243149',
          'editor.foreground': '#e9edf7',
        },
        tokenColors: [
          {
            scope: [
              'keyword',
              'storage',
              'support.function',
              'entity.name.function',
            ],
            settings: { foreground: '#d5b8ef' },
          },
          { scope: ['string'], settings: { foreground: '#edc5d8' } },
          {
            scope: ['variable', 'support.type.property-name.json'],
            settings: { foreground: '#add8f1' },
          },
          {
            scope: ['constant.numeric', 'constant.language'],
            settings: { foreground: '#ead4ac' },
          },
          { scope: ['punctuation'], settings: { foreground: '#b9c4da' } },
        ],
      },
    ],
  })
  const engine = await highlighter
  // Our cURL templates contain a JSON body inside -d's single-quoted argument.
  // Tokenize that body as JSON so keys and values remain visually distinct.
  const bodyMarker = "  -d '"
  const bodyStart = code.indexOf(bodyMarker)
  const segments =
    bodyStart >= 0 && code.endsWith("'")
      ? [
          {
            code: code.slice(0, bodyStart + bodyMarker.length),
            lang: 'shellscript',
          },
          { code: code.slice(bodyStart + bodyMarker.length, -1), lang: 'json' },
          { code: "'", lang: 'shellscript' },
        ]
      : [{ code, lang: 'shellscript' }]
  const result: IntegrationCodeToken[] = []
  let offset = 0
  for (const segment of segments) {
    const lines = engine.codeToTokens(segment.code, {
      lang: segment.lang,
      theme: 'silvermoon-code',
    }).tokens
    for (const [index, line] of lines.entries()) {
      if (index > 0) {
        result.push({ content: '\n', color: '#e9edf7', offset })
        offset++
      }
      for (const token of line) {
        if (!token.content) continue
        result.push({
          content: token.content,
          color: token.color ?? '#e9edf7',
          offset,
        })
        offset += token.content.length
      }
    }
  }
  return result
}
