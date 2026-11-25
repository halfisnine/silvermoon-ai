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
import { describe, expect, test } from 'vitest'

import {
  formatMarketingDate,
  formatMarketingMoney,
  formatMarketingDuration,
} from '../format'

describe('marketing display amounts', () => {
  test.each(['zhCN', 'zhTW', 'en', 'fr', 'ja', 'ru', 'vi', 'invalid_locale'])(
    'preserves tiny and large exact amounts for %s',
    (language) => {
      const tiny = formatMarketingMoney('0.000000000001', 'CNY', language)
      expect(tiny).toMatch(/000000000001/)
      expect(
        formatMarketingMoney('9007199254740991.123456789012', 'CNY', language)
      ).toContain('123456789012')
      expect(formatMarketingMoney('2.5', 'CNY', language)).not.toContain(
        '500000'
      )
    }
  )
  test('Chinese dates use month/day words with the activity timezone', () => {
    const stamp = Date.parse('2026-10-20T00:30:00+08:00') / 1000
    expect(formatMarketingDate(stamp, 'zhCN')).toContain('2026年10月20日')
    expect(formatMarketingDate(stamp, 'zhTW')).toContain('2026年10月20日')
    expect(formatMarketingDate(stamp, 'en')).toContain('October 20, 2026')
  })
  test('a historical CNY amount keeps CNY when site settings differ', () => {
    expect(formatMarketingMoney('100', 'CNY', 'en')).toContain('¥100')
    expect(formatMarketingMoney('100', 'USD', 'en')).toBe('$100')
  })
})

test.each(['zhCN', 'zhTW', 'en', 'fr', 'ja', 'ru', 'vi', 'invalid_locale'])(
  'duration unit formatting accepts %s and preserves sub-minute validity',
  (language) => {
    expect(formatMarketingDuration(6, language)).toContain('6')
    expect(formatMarketingDuration(60, language)).toContain('1')
    expect(formatMarketingDuration(90, language)).toContain('90')
    expect(formatMarketingDuration(3600, language)).toContain('1')
    expect(formatMarketingDuration(86400, language)).toContain('1')
  }
)
