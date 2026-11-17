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
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { CodeBlockFrame } from '@/components/ai-elements/code-block'
import { CopyButton } from '@/components/copy-button'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'

import {
  buildIntegrationExample,
  INTEGRATION_PROTOCOLS,
} from '../lib/integration-example'

interface SilvermoonIntegrationProps {
  endpoint: string
  loading: boolean
  description?: string
  children: ReactNode
}

export function SilvermoonIntegration(props: SilvermoonIntegrationProps) {
  const { t } = useTranslation()
  const example = buildIntegrationExample(props.endpoint)

  return (
    <section
      className='silvermoon-integration'
      aria-labelledby='integration-title'
    >
      <header className='silvermoon-integration-heading'>
        <p className='silvermoon-kicker'>{t('Quick integration')}</p>
        <h2 id='integration-title'>{t('Your next request starts here.')}</h2>
        <p>{t('Connect with OpenAI, Anthropic or Gemini protocols.')}</p>
      </header>
      <div className='silvermoon-integration-panel'>
        {props.loading && <Skeleton className='h-48 w-full' />}
        {!props.loading && props.endpoint && (
          <div className='silvermoon-endpoint'>
            <div className='min-w-0'>
              <span className='silvermoon-endpoint-label'>API URL</span>
              <code>{props.endpoint}</code>
            </div>
            <CopyButton
              value={props.endpoint}
              variant='outline'
              size='default'
              aria-label={t('Copy API address')}
            >
              {t('Copy')}
            </CopyButton>
          </div>
        )}
        {!props.loading && !props.endpoint && (
          <p className='silvermoon-empty-endpoint'>
            {t('No API routes configured')}
          </p>
        )}
        {props.description && (
          <p className='silvermoon-endpoint-hint'>{props.description}</p>
        )}
        {!props.loading && example && (
          <>
            <Tabs defaultValue='openai' className='silvermoon-protocol-tabs'>
              <TabsList aria-label={t('API protocol')}>
                {INTEGRATION_PROTOCOLS.map((protocol) => (
                  <TabsTrigger key={protocol.id} value={protocol.id}>
                    {protocol.label}
                  </TabsTrigger>
                ))}
              </TabsList>
              {INTEGRATION_PROTOCOLS.map((protocol) => {
                const code = buildIntegrationExample(
                  props.endpoint,
                  protocol.id
                )
                return (
                  <TabsContent key={protocol.id} value={protocol.id}>
                    <CodeBlockFrame
                      className='silvermoon-integration-code'
                      showToolbar
                      title='cURL'
                      endActions={
                        <CopyButton
                          value={code}
                          size='default'
                          aria-label={t('Copy code')}
                        >
                          {t('Copy code')}
                        </CopyButton>
                      }
                    >
                      <pre tabIndex={0} aria-label={t('cURL request example')}>
                        <code>{code}</code>
                      </pre>
                    </CodeBlockFrame>
                  </TabsContent>
                )
              })}
            </Tabs>
            <p className='silvermoon-endpoint-hint'>
              {t(
                'Set SILVERMOON_API_KEY to your API key and replace YOUR_MODEL_NAME with a model that supports the selected protocol.'
              )}
            </p>
          </>
        )}
        <div className='silvermoon-integration-bottom'>{props.children}</div>
      </div>
    </section>
  )
}
