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
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { useAuthStore } from '@/stores/auth-store'

import { getUserActivities } from '../api'

export function WalletActivityNotice() {
  const userId = useAuthStore((state) => state.auth.user?.id)

  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['marketing-user', userId, 'wallet-notice'],
    queryFn: () => getUserActivities(1, 'recharge', 'active'),
    refetchInterval: 30000,
  })
  if (!query.data?.items.length) return null
  return (
    <aside className='mb-3 rounded-xl border p-3 text-sm'>
      <p>
        {t(
          'Recharge activities are available. Rewards depend on the activity rules.'
        )}
      </p>
      {query.data.items.some((item) => item.pools.length > 0) && (
        <p>
          {t(
            'Draw opportunities are limited. Payment does not reserve a place.'
          )}
        </p>
      )}
      <Link
        to='/activities/center'
        className='text-primary mt-1 inline-block underline'
      >
        {t('View activity details')}
      </Link>
    </aside>
  )
}
