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
import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import type { ComponentProps } from 'react'
import { I18nextProvider, initReactI18next } from 'react-i18next'
import { beforeEach, expect, it, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'

import { RechargeFormCard } from '../components/recharge-form-card'

const i18n = createInstance()
const shopUrl = 'https://shop.example.com/silvermoon'

beforeEach(async () => {
  await i18n.use(initReactI18next).init({
    lng: 'en',
    fallbackLng: false,
    nsSeparator: false,
    resources: { en, zh },
    interpolation: { escapeValue: false },
  })
})

function wallet(
  overrides: Partial<ComponentProps<typeof RechargeFormCard>> = {}
) {
  return (
    <I18nextProvider i18n={i18n}>
      <RechargeFormCard
        topupInfo={{
          enable_online_topup: true,
          enable_stripe_topup: false,
          enable_redemption: true,
          pay_methods: [{ name: 'Alipay', type: 'alipay' }],
          min_topup: 1,
          stripe_min_topup: 1,
          amount_options: [],
          discount: {},
        }}
        presetAmounts={[]}
        selectedPreset={null}
        topupAmount={0}
        paymentAmount={0}
        calculating={false}
        paymentLoading={null}
        redemptionCode=''
        redeeming={false}
        topupLink={shopUrl}
        onSelectPreset={vi.fn()}
        onTopupAmountChange={vi.fn()}
        onPaymentMethodSelect={vi.fn()}
        onRedemptionCodeChange={vi.fn()}
        onRedeem={vi.fn()}
        {...overrides}
      />
    </I18nextProvider>
  )
}

it.each([undefined, '', '   ', 'not a URL', 'javascript:alert(1)'])(
  'hides the shop card when the configured URL is %s',
  (topupLink) => {
    render(wallet({ topupLink }))
    expect(
      screen.queryByRole('link', { name: /WeChat Pay/ })
    ).not.toBeInTheDocument()
    expect(screen.getByLabelText('Have a Code?')).toBeVisible()
  }
)

it('updates the destination when configuration changes and hides it when cleared', () => {
  const { rerender } = render(wallet({ topupLink: '' }))
  rerender(wallet({ topupLink: `  ${shopUrl}  ` }))
  expect(screen.getByRole('link', { name: /WeChat Pay/ })).toHaveAttribute(
    'href',
    shopUrl
  )
  rerender(wallet({ topupLink: 'https://shop.example.com/new' }))
  expect(screen.getByRole('link', { name: /WeChat Pay/ })).toHaveAttribute(
    'href',
    'https://shop.example.com/new'
  )
  rerender(wallet({ topupLink: '' }))
  expect(
    screen.queryByRole('link', { name: /WeChat Pay/ })
  ).not.toBeInTheDocument()
})

it('opens the shop independently of zero amount and payment loading without creating a payment', async () => {
  const onPaymentMethodSelect = vi.fn()
  render(wallet({ paymentLoading: 'alipay', onPaymentMethodSelect }))
  const link = screen.getByRole('link', { name: /WeChat Pay/ })
  expect(link).toHaveAttribute('target', '_blank')
  expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  expect(link).not.toHaveAttribute('aria-disabled', 'true')
  expect(screen.getByRole('button', { name: /Alipay/ })).toBeDisabled()
  link.addEventListener('click', (event) => event.preventDefault())
  link.focus()
  await userEvent.setup().keyboard('{Enter}')
  expect(onPaymentMethodSelect).not.toHaveBeenCalled()
})

it('keeps the shop available without online payments and hides it when redemption is disabled', () => {
  const { rerender } = render(wallet({ topupInfo: null }))
  expect(screen.getByRole('link', { name: /WeChat Pay/ })).toBeVisible()
  rerender(
    wallet({
      topupInfo: {
        enable_online_topup: false,
        enable_stripe_topup: false,
        enable_redemption: false,
        pay_methods: [],
        min_topup: 1,
        stripe_min_topup: 1,
        amount_options: [],
        discount: {},
      },
    })
  )
  expect(
    screen.queryByRole('link', { name: /WeChat Pay/ })
  ).not.toBeInTheDocument()
})

it('preserves payment and redemption actions and translates the shop card', async () => {
  const onPaymentMethodSelect = vi.fn()
  const onRedeem = vi.fn()
  render(
    wallet({
      topupAmount: 10,
      redemptionCode: 'test-code',
      onPaymentMethodSelect,
      onRedeem,
    })
  )
  fireEvent.click(screen.getByRole('button', { name: 'Alipay' }))
  expect(onPaymentMethodSelect).toHaveBeenCalledWith({
    name: 'Alipay',
    type: 'alipay',
  })
  fireEvent.click(screen.getByRole('button', { name: 'Redeem' }))
  expect(onRedeem).toHaveBeenCalledOnce()
  expect(screen.queryByText('Get one here')).not.toBeInTheDocument()
  await act(() => i18n.changeLanguage('zh'))
  expect(
    screen.getByRole('link', { name: /微信支付 · 兑换码/ })
  ).toHaveTextContent('在店铺选择面额，购买后回本站兑换。')
})
