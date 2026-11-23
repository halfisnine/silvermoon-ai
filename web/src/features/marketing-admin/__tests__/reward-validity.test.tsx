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
import { act, render, screen } from '@testing-library/react'
import i18next from 'i18next'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { expect, test } from 'vitest'

import { RewardSummary } from '../activities/summary'

test.each([
  [6, '6 seconds'],
  [60, '1 minute'],
  [3600, '1 hour'],
  [86400, '1 day'],
])('a %s-second gift shows an exact nonzero validity', (seconds, expected) => {
  render(
    <RewardSummary
      rewards={[
        {
          kind: 'gift',
          calculation: 'fixed',
          amount: '2',
          currency: 'CNY',
          valid_seconds: Number(seconds),
        },
      ]}
    />
  )
  expect(screen.getByText(`¥2 · Validity: ${expected}`)).toBeVisible()
})

test('gift validity responds to language changes while keeping the exact six-second duration', async () => {
  const instance = i18next.createInstance()
  await instance
    .use(initReactI18next)
    .init({
      lng: 'en',
      fallbackLng: 'en',
      resources: {
        en: { translation: {} },
        zhCN: {
          translation: { 'Validity: {{duration}}': '有效期：{{duration}}' },
        },
      },
    })
  render(
    <I18nextProvider i18n={instance}>
      <RewardSummary
        rewards={[
          {
            kind: 'gift',
            calculation: 'fixed',
            amount: '2',
            currency: 'CNY',
            valid_seconds: 6,
          },
        ]}
      />
    </I18nextProvider>
  )
  expect(screen.getByText('¥2 · Validity: 6 seconds')).toBeVisible()
  await act(async () => {
    await instance.changeLanguage('zhCN')
  })
  expect(screen.getByText('¥2 · 有效期：6秒钟')).toBeVisible()
})
