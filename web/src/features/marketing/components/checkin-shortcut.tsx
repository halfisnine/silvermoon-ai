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
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'

export function CheckinShortcut() {
  const { t } = useTranslation()
  return (
    <Card className='gap-3 p-4'>
      <h3 className='font-semibold'>{t('Check-in for rewards')}</h3>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Check-in activities and make-up dates are now available in Activities.'
        )}
      </p>
      <Button role='link' render={<Link to='/activities/checkin' />}>
        {t('Go to check-in')}
      </Button>
    </Card>
  )
}
