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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'

import { getLotteryDraw, submitLotteryDraw } from './api'

const memory = new Map<string, string>()
/** Only an opaque recovery key is persisted; the server owns identity, outcome and balances. */
export function useLotteryDraw(userId: number, poolId: number) {
  const storage = `silvermoon:lottery:v1:${userId}:${poolId}`
  const [key, setKey] = useState(() => {
    try {
      return localStorage.getItem(storage) || memory.get(storage) || ''
    } catch {
      return memory.get(storage) || ''
    }
  })
  const [error, setError] = useState<unknown>(null)
  const inFlight = useRef(false)
  const client = useQueryClient()
  const result = useQuery({
    queryKey: ['marketing', 'draw', userId, poolId, key],
    queryFn: () => getLotteryDraw(key),
    enabled: !!key,
    retry: false,
    refetchInterval: (query) =>
      query.state.data?.state === 'pending' ? 3000 : false,
    meta: { errorToast: false },
  })
  useEffect(() => {
    if (
      result.data?.state === 'completed' ||
      result.data?.state === 'cleared'
    ) {
      void client.invalidateQueries({
        queryKey: ['marketing', 'lottery', userId],
      })
      void client.invalidateQueries({ queryKey: ['marketing-user', userId] })
    }
  }, [client, userId, result.data?.id, result.data?.state])
  const mutation = useMutation({
    mutationFn: (operation: string) => submitLotteryDraw(poolId, operation),
    onSuccess: async (draw, operation) => {
      await client.cancelQueries({
        queryKey: ['marketing', 'draw', userId, poolId, operation],
      })
      client.setQueryData(
        ['marketing', 'draw', userId, poolId, operation],
        draw
      )
      void client.invalidateQueries({ queryKey: ['marketing', 'lottery'] })
      void client.invalidateQueries({ queryKey: ['marketing-user', userId] })
    },
    onError: (failure) => {
      setError(failure)
    },
    onSettled: () => {
      inFlight.current = false
    },
  })
  const start = () => {
    if (inFlight.current || result.data) return
    inFlight.current = true
    setError(null)
    const operation = key || crypto.randomUUID()
    // Persist before posting, so a reload or navigation can only recover/retry this attempt.
    memory.set(storage, operation)
    try {
      localStorage.setItem(storage, operation)
    } catch {
      /* Memory still protects navigation within this tab. */
    }
    setKey(operation)
    mutation.mutate(operation)
  }
  const acknowledge = () => {
    if (!result.data || result.data.state === 'pending') return
    memory.delete(storage)
    try {
      localStorage.removeItem(storage)
    } catch {
      /* No sensitive data is stored. */
    }
    setKey('')
    setError(null)
    mutation.reset()
  }
  return {
    result: result.data,
    recovering: !!key && result.isPending,
    busy: mutation.isPending,
    pendingKey: !!key,
    error: error || result.error,
    start,
    acknowledge,
    refresh: () => {
      setError(null)
      void result.refetch()
    },
  }
}
