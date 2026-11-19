// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import type { ApiClient } from '@ostendorf-cloud/govue'
import { inject, type InjectionKey } from 'vue'

import { sharedAuth } from './auth'
import type {
  Direction,
  List,
  ListDetail,
  ListOverview,
  ListRequest,
  Wish,
  WishRequest,
} from './types'

/** The owner API. Every call is for whoever is signed in: no id of an owner. */
export interface OwnerApi {
  lists(options?: { archived?: boolean }): Promise<ListOverview>
  list(id: number, options?: { archived?: boolean }): Promise<ListDetail>
  createList(body: ListRequest): Promise<List>
  updateList(id: number, body: ListRequest): Promise<List>
  archiveList(id: number, archived: boolean): Promise<List>
  deleteList(id: number): Promise<void>
  wish(id: number): Promise<Wish>
  createWish(listId: number, body: WishRequest): Promise<Wish>
  updateWish(id: number, body: WishRequest): Promise<Wish>
  archiveWish(id: number, archived: boolean): Promise<Wish>
  deleteWish(id: number): Promise<void>
  moveWish(id: number, direction: Direction): Promise<void>
  relocateWish(id: number, listId: number): Promise<Wish>
}

export const ownerApiKey: InjectionKey<OwnerApi> = Symbol('owner-api')

const base = '/owner'

export function createOwnerApi(client: ApiClient): OwnerApi {
  const query = (archived?: boolean) => (archived ? { archived: true } : undefined)
  return {
    lists: (options) => client.get(`${base}/lists`, { query: query(options?.archived) }),
    list: (id, options) => client.get(`${base}/lists/${id}`, { query: query(options?.archived) }),
    createList: (body) => client.post(`${base}/lists`, body),
    updateList: (id, body) => client.put(`${base}/lists/${id}`, body),
    archiveList: (id, archived) => client.put(`${base}/lists/${id}/archived`, { archived }),
    deleteList: (id) => client.del(`${base}/lists/${id}`),
    wish: (id) => client.get(`${base}/wishes/${id}`),
    createWish: (listId, body) => client.post(`${base}/lists/${listId}/wishes`, body),
    updateWish: (id, body) => client.put(`${base}/wishes/${id}`, body),
    archiveWish: (id, archived) => client.put(`${base}/wishes/${id}/archived`, { archived }),
    deleteWish: (id) => client.del(`${base}/wishes/${id}`),
    moveWish: (id, direction) => client.post(`${base}/wishes/${id}/move`, { direction }),
    relocateWish: (id, listId) => client.put(`${base}/wishes/${id}/list`, { list_id: listId }),
  }
}

/** The client of the running app: same origin, the session cookie, the session's token. */
export function createDefaultOwnerApi(): OwnerApi {
  return createOwnerApi(sharedAuth().client)
}

let shared: OwnerApi | undefined

/** The API a view uses: the one a parent provided, a fake in a test, else the real one. */
export function useOwnerApi(): OwnerApi {
  return inject(ownerApiKey, () => (shared ??= createDefaultOwnerApi()), true)
}
