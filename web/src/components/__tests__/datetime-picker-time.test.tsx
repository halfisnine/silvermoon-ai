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
import i18next from 'i18next'
import { expect, test, vi } from 'vitest'

import { DateTimePicker } from '../datetime-picker'

test('shows the seconds of the selected time', () => {
  render(
    <DateTimePicker
      value={new Date(2026, 9, 11, 8, 30, 45)}
      onChange={() => undefined}
    />
  )
  expect(screen.getByLabelText('Time')).toHaveValue('08:30:45')
})

test('keeps the seconds typed into the time field', () => {
  const onChange = vi.fn()
  render(
    <DateTimePicker
      value={new Date(2026, 9, 11, 8, 0, 0)}
      onChange={onChange}
    />
  )
  fireEvent.change(screen.getByLabelText('Time'), {
    target: { value: '08:30:45' },
  })
  expect(onChange).toHaveBeenLastCalledWith(new Date(2026, 9, 11, 8, 30, 45))
})

test('reads a time without seconds as zero seconds', () => {
  const onChange = vi.fn()
  render(
    <DateTimePicker
      value={new Date(2026, 9, 11, 8, 0, 30)}
      onChange={onChange}
    />
  )
  fireEvent.change(screen.getByLabelText('Time'), {
    target: { value: '09:15' },
  })
  expect(onChange).toHaveBeenLastCalledWith(new Date(2026, 9, 11, 9, 15, 0))
})

test('keeps the current time when the time field is emptied', () => {
  const onChange = vi.fn()
  render(
    <DateTimePicker
      value={new Date(2026, 9, 11, 20, 30, 45)}
      onChange={onChange}
    />
  )
  const field = screen.getByLabelText('Time')
  fireEvent.change(field, { target: { value: '' } })
  expect(onChange).not.toHaveBeenCalled()
  expect(field).toHaveValue('20:30:45')
})

test('forwards form labels and errors to the date control', () => {
  render(
    <>
      <label htmlFor='starts-at'>Activity starts</label>
      <p id='date-error'>Choose a date</p>
      <DateTimePicker
        id='starts-at'
        aria-describedby='date-error'
        aria-invalid
      />
    </>
  )
  expect(
    screen.getByRole('button', { name: 'Activity starts' })
  ).toHaveAttribute('aria-invalid', 'true')
  expect(
    screen.getByRole('button', { name: 'Activity starts' })
  ).toHaveAccessibleDescription('Choose a date')
})

test('activity dates use the specified timezone even outside that timezone', () => {
  const onChange = vi.fn()
  render(
    <DateTimePicker
      timeZone='Asia/Shanghai'
      value={new Date('2026-10-20T00:30:00Z')}
      onChange={onChange}
    />
  )
  expect(screen.getByLabelText('Time')).toHaveValue('08:30:00')
  fireEvent.change(screen.getByLabelText('Time'), {
    target: { value: '09:00:00' },
  })
  expect(onChange).toHaveBeenLastCalledWith(new Date('2026-10-20T01:00:00Z'))
})

test('Chinese month/day labels update when switching language', async () => {
  render(<DateTimePicker value={new Date(2026, 9, 20, 9)} />)
  try {
    await act(async () => {
      await i18next.changeLanguage('zhCN')
    })
    expect(screen.getByRole('button', { name: '2026年10月20日' })).toBeVisible()
    await act(async () => {
      await i18next.changeLanguage('en')
    })
    expect(
      screen.getByRole('button', { name: 'October 20, 2026' })
    ).toBeVisible()
  } finally {
    await act(async () => {
      await i18next.changeLanguage('en')
    })
  }
})

test('compact picker keeps time inside the popup, preserves timezone and seconds, and clears the filter', async () => {
  const onChange = vi.fn()
  render(
    <DateTimePicker
      compact
      aria-label='Start date'
      timeZone='Asia/Shanghai'
      value={new Date('2026-10-20T00:30:45Z')}
      onChange={onChange}
    />
  )
  const user = userEvent.setup()
  const trigger = screen.getByRole('button', { name: 'Start date' })
  expect(trigger).toHaveTextContent('October 20, 2026 08:30:45')
  expect(screen.queryByLabelText('Time')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Clear' })
  ).not.toBeInTheDocument()
  await user.click(trigger)
  expect(await screen.findByLabelText('Time')).toHaveValue('08:30:45')
  fireEvent.change(screen.getByLabelText('Time'), {
    target: { value: '09:10:25' },
  })
  expect(onChange).toHaveBeenLastCalledWith(new Date('2026-10-20T01:10:25Z'))
  await user.click(screen.getByRole('button', { name: 'Done' }))
  expect(screen.queryByLabelText('Time')).not.toBeInTheDocument()
  expect(trigger).toHaveTextContent('09:10:25')
  await user.click(trigger)
  await user.click(await screen.findByRole('button', { name: 'Clear' }))
  expect(onChange).toHaveBeenLastCalledWith(undefined)
  expect(trigger).toHaveTextContent('Select date')
})

test('compact date selection stays open for time entry and date-only mode omits the time', async () => {
  const onChange = vi.fn()
  const view = render(
    <DateTimePicker
      compact
      aria-label='Start date'
      value={new Date(2026, 9, 20, 8, 30)}
      onChange={onChange}
    />
  )
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Start date' }))
  await user.click(screen.getByText('21', { selector: 'button' }))
  expect(onChange).toHaveBeenLastCalledWith(new Date(2026, 9, 21, 8, 30))
  expect(screen.getByLabelText('Time')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Done' }))
  view.rerender(
    <DateTimePicker
      compact
      hideTime
      aria-label='Start date'
      value={new Date(2026, 9, 20, 8, 30)}
      onChange={onChange}
    />
  )
  expect(
    screen.getByRole('button', { name: 'Start date' })
  ).not.toHaveTextContent('08:30')
  await user.click(screen.getByRole('button', { name: 'Start date' }))
  expect(screen.queryByLabelText('Time')).not.toBeInTheDocument()
})
