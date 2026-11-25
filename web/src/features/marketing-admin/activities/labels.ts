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
export function activityKindLabel(
  kind: string,
  t: (key: string) => string
): string {
  switch (kind) {
    case 'registration':
      return t('Registration gifts')
    case 'checkin':
      return t('Check-in rewards')
    case 'recharge':
      return t('Recharge rewards')
    case 'invitation':
      return t('Invitation rewards')
    default:
      return t('Activity')
  }
}
export function activityStateLabel(
  state: string,
  t: (key: string) => string
): string {
  switch (state) {
    case 'draft':
      return t('Draft')
    case 'scheduled':
      return t('Scheduled')
    case 'active':
      return t('Active')
    case 'paused':
      return t('Paused')
    case 'ended':
      return t('Ended')
    default:
      return t('Unavailable')
  }
}
