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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'

import { getMarketingMetadata } from '../api'
import { GiftBatchDetail } from './batch-detail'
import { AdminGiftDetail } from './gift-detail'
import { GiftIssueDialog } from './issue-dialog'
import { GiftRecords } from './records'

export function MarketingGiftsPage() {
  const { t } = useTranslation()
  const [tab, setTab] = useState('gifts')
  const [issuing, setIssuing] = useState(false)
  const [batchID, setBatchID] = useState<number>()
  const [giftID, setGiftID] = useState<number>()
  const metadata = useQuery({
    queryKey: ['marketing-admin', 'metadata'],
    queryFn: getMarketingMetadata,
  })
  return (
    <>
      <SectionPageLayout fixedContent>
        <SectionPageLayout.Title>
          {t('Gift management')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button disabled={!metadata.data} onClick={() => setIssuing(true)}>
            {t('Issue gift credits')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='flex h-full min-h-0 flex-col gap-3'>
            <Tabs value={tab} onValueChange={(v) => setTab(String(v))}>
              <TabsList>
                <TabsTrigger value='gifts'>{t('All gift credits')}</TabsTrigger>
                <TabsTrigger value='batches'>
                  {t('Manual issuance records')}
                </TabsTrigger>
              </TabsList>
            </Tabs>
            {metadata.isError && (
              <ErrorState onRetry={() => void metadata.refetch()} />
            )}
            <GiftRecords
              key={tab}
              batches={tab === 'batches'}
              onBatch={setBatchID}
              onGift={setGiftID}
            />
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>
      {issuing && metadata.data && (
        <GiftIssueDialog
          currency={metadata.data.currency}
          moneyRevision={metadata.data.money_revision}
          onClose={() => setIssuing(false)}
          onIssued={(batch) => {
            setIssuing(false)
            setTab('batches')
            setBatchID(batch.id)
          }}
        />
      )}
      {batchID && (
        <GiftBatchDetail id={batchID} onClose={() => setBatchID(undefined)} />
      )}
      {giftID && (
        <AdminGiftDetail
          id={giftID}
          onClose={() => setGiftID(undefined)}
          onBatch={(id) => {
            setGiftID(undefined)
            setBatchID(id)
          }}
        />
      )}
    </>
  )
}
