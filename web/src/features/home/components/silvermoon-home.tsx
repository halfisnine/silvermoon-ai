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
import { ArrowUpRight } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { AnnouncementsPanel } from '@/features/dashboard/components/overview/announcements-panel'
import { useApiInfo } from '@/features/dashboard/hooks/use-status-data'
import { useStatus } from '@/hooks/use-status'
import { isHttpUrl } from '@/lib/content-format'

import { SilvermoonIntegration } from './silvermoon-integration'

export function SilvermoonHome() {
  const { t } = useTranslation()
  const [guideOpen, setGuideOpen] = useState(false)
  const { status, loading } = useStatus()
  const { items } = useApiInfo()
  // Keep configured route paths intact: different clients expect different base URLs.
  const route = items.find(
    (item) => typeof item.url === 'string' && isHttpUrl(item.url)
  )
  const serverAddress =
    typeof status?.server_address === 'string' ? status.server_address : ''
  const endpoint =
    status?.api_info_enabled === false
      ? ''
      : route?.url || (isHttpUrl(serverAddress) ? serverAddress : '')
  const docsLink =
    typeof status?.docs_link === 'string' && isHttpUrl(status.docs_link)
      ? status.docs_link
      : null

  return (
    <main id='main' className='silvermoon-home'>
      <section className='silvermoon-hero' aria-labelledby='silvermoon-title'>
        <img
          className='silvermoon-hero-art'
          src='/silvermoon/moonlight.webp'
          alt=''
          width={1254}
          height={1254}
          fetchPriority='high'
        />
        <div className='silvermoon-home-width silvermoon-hero-content'>
          <p className='silvermoon-eyebrow'>{t('Model API gateway')}</p>
          <h1 id='silvermoon-title'>
            {t('Connect to models.')}
            <br />
            <em>{t('Let inspiration begin.')}</em>
          </h1>
          <p className='silvermoon-hero-description'>
            {t('Bring AI models to your apps and everyday tools.')}
          </p>
          <div className='silvermoon-hero-actions'>
            <Button size='lg' render={<Link to='/dashboard' />}>
              {t('Go to Dashboard')}
              <ArrowUpRight aria-hidden='true' />
            </Button>
            <Button size='lg' variant='outline' render={<Link to='/pricing' />}>
              {t('Models and pricing')}
              <ArrowUpRight aria-hidden='true' />
            </Button>
          </div>
          <Button
            variant='link'
            className='silvermoon-guide-link'
            render={
              docsLink ? (
                <a href={docsLink} target='_blank' rel='noopener noreferrer' />
              ) : undefined
            }
            onClick={docsLink ? undefined : () => setGuideOpen(true)}
          >
            {t('First visit? Read the setup guide')}
            <ArrowUpRight aria-hidden='true' />
          </Button>
        </div>
        <span className='silvermoon-art-caption' aria-hidden='true'>
          {t('Under the same moon.')}
        </span>
      </section>
      <div className='silvermoon-home-width silvermoon-home-info'>
        <SilvermoonIntegration
          endpoint={endpoint}
          loading={loading}
          description={route?.description}
        >
          <div className='silvermoon-support-actions'>
            <Button
              variant='outline'
              render={
                docsLink ? (
                  <a
                    href={docsLink}
                    target='_blank'
                    rel='noopener noreferrer'
                  />
                ) : undefined
              }
              onClick={docsLink ? undefined : () => setGuideOpen(true)}
            >
              {docsLink ? t('Docs') : t('Setup guide')}
              <ArrowUpRight aria-hidden='true' />
            </Button>
            <Button variant='outline' render={<Link to='/about' />}>
              {t('About')}
              <ArrowUpRight aria-hidden='true' />
            </Button>
          </div>
        </SilvermoonIntegration>
        {status?.announcements_enabled !== false && (
          <AnnouncementsPanel compact />
        )}
      </div>
      <Dialog
        open={guideOpen}
        onOpenChange={setGuideOpen}
        title={t('Setup guide')}
      >
        <ol className='list-decimal space-y-4 pl-5 text-sm'>
          <li>
            <Link className='underline' to='/pricing'>
              {t('Model Square')}
            </Link>
            <p className='text-muted-foreground mt-1'>
              {t('Review model rates before scaling traffic')}
            </p>
          </li>
          <li>
            <Link className='underline' to='/keys'>
              {t('Create API Key')}
            </Link>
            <p className='text-muted-foreground mt-1'>
              {t('Create a key for your app or service')}
            </p>
          </li>
          <li>
            <Link className='underline' to='/playground'>
              {t('Send a request')}
            </Link>
            <p className='text-muted-foreground mt-1'>
              {t('Verify routing with Playground or your client')}
            </p>
          </li>
        </ol>
      </Dialog>
    </main>
  )
}
