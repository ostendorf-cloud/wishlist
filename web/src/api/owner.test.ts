// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { createClient } from '@ostendorf-cloud/govue'
import { describe, expect, it } from 'vitest'

import { createOwnerApi } from './owner'
import type { ListRequest, WishRequest } from './types'

interface Seen {
  method: string
  url: string
  body: unknown
  headers: Record<string, string>
}

/** An API over a fetch that records every request and answers 200 with {}. */
function recorded(status = 200, payload: unknown = {}) {
  const seen: Seen[] = []
  const fetch = (url: string | URL | Request, init?: RequestInit): Promise<Response> => {
    seen.push({
      method: init?.method ?? 'GET',
      url: url instanceof Request ? url.url : url.toString(),
      body: typeof init?.body === 'string' ? JSON.parse(init.body) : undefined,
      headers: { ...(init?.headers as Record<string, string>) },
    })
    return Promise.resolve(
      status === 204
        ? new Response(null, { status })
        : new Response(JSON.stringify(payload), {
            status,
            headers: { 'Content-Type': 'application/json' },
          }),
    )
  }
  const api = createOwnerApi(createClient({ fetch, csrfToken: () => 'token-1' }))
  return { api, seen }
}

const listBody: ListRequest = {
  title: 'Birthday',
  occasion: '',
  greeting: '',
  event_date: '',
  reserve_open: true,
}
const wishBody: WishRequest = {
  title: 'Book',
  description: '',
  url: '',
  shop: '',
  price_cents: 1990,
  currency: 'EUR',
  priority: 1,
  quantity: 1,
  group_gift: false,
}

describe('owner api', () => {
  it('maps every call to its endpoint, method and body', async () => {
    const { api, seen } = recorded()
    await api.lists()
    await api.lists({ archived: true })
    await api.list(3)
    await api.list(3, { archived: true })
    await api.createList(listBody)
    await api.updateList(3, listBody)
    await api.archiveList(3, true)
    await api.deleteList(3)
    await api.wish(4)
    await api.createWish(3, wishBody)
    await api.updateWish(4, wishBody)
    await api.archiveWish(4, false)
    await api.deleteWish(4)
    await api.moveWish(4, 'up')
    await api.relocateWish(4, 7)

    expect(seen.map((s) => [s.method, s.url, s.body])).toEqual([
      ['GET', '/api/v1/owner/lists', undefined],
      ['GET', '/api/v1/owner/lists?archived=true', undefined],
      ['GET', '/api/v1/owner/lists/3', undefined],
      ['GET', '/api/v1/owner/lists/3?archived=true', undefined],
      ['POST', '/api/v1/owner/lists', listBody],
      ['PUT', '/api/v1/owner/lists/3', listBody],
      ['PUT', '/api/v1/owner/lists/3/archived', { archived: true }],
      ['DELETE', '/api/v1/owner/lists/3', undefined],
      ['GET', '/api/v1/owner/wishes/4', undefined],
      ['POST', '/api/v1/owner/lists/3/wishes', wishBody],
      ['PUT', '/api/v1/owner/wishes/4', wishBody],
      ['PUT', '/api/v1/owner/wishes/4/archived', { archived: false }],
      ['DELETE', '/api/v1/owner/wishes/4', undefined],
      ['POST', '/api/v1/owner/wishes/4/move', { direction: 'up' }],
      ['PUT', '/api/v1/owner/wishes/4/list', { list_id: 7 }],
    ])
  })

  it('never names an owner, in a body or in a query', async () => {
    const { api, seen } = recorded()
    await api.lists({ archived: true })
    await api.createList(listBody)
    await api.createWish(3, wishBody)
    await api.relocateWish(4, 7)

    const text = JSON.stringify(seen).toLowerCase()
    expect(text).not.toMatch(/owner_?id|"owner"|[?&]owner/)
  })

  it('sends the CSRF token on writes and not on reads', async () => {
    const { api, seen } = recorded()
    await api.lists()
    await api.deleteList(3)

    expect(seen[0]?.headers['X-CSRF-Token']).toBeUndefined()
    expect(seen[1]?.headers['X-CSRF-Token']).toBe('token-1')
  })

  it('sends JSON as the content type of a body', async () => {
    const { api, seen } = recorded()
    await api.createList(listBody)

    expect(seen[0]?.headers['Content-Type']).toBe('application/json')
  })

  it('answers undefined for a 204', async () => {
    const { api } = recorded(204)
    await expect(api.deleteWish(4)).resolves.toBeUndefined()
  })

  it('turns an error answer into an ApiError with the field details', async () => {
    const { api } = recorded(400, {
      error: {
        code: 'invalid_argument',
        message: 'bad',
        details: { title: 'required' },
        request_id: 'r1',
      },
    })
    await expect(api.createList(listBody)).rejects.toMatchObject({
      code: 'invalid_argument',
      status: 400,
      details: { title: 'required' },
      requestId: 'r1',
    })
  })
})
