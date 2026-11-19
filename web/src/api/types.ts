// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/**
 * The owner API, as internal/webapp/owner/dto sends and receives it. This file
 * is the one place the wire shape is written down: the API module, the views
 * and the messages module all import from here. Nothing in it can carry what a
 * guest took, and no request names an owner: the server knows who is signed in.
 */

export interface List {
  id: number
  title: string
  occasion: string
  greeting: string
  /** YYYY-MM-DD, or null. */
  event_date: string | null
  /** The owner's switch: whether guests may still take wishes. */
  reserve_open: boolean
  archived: boolean
  /** Whether a guest link exists. */
  shared: boolean
  /** Live wishes; filled by the overview only. */
  wish_count: number
  created_at: string
  updated_at: string
}

export interface Wish {
  id: number
  list_id: number
  title: string
  description: string
  url: string
  shop: string
  price_cents: number | null
  currency: string
  /** 0 an idea, 1 normal, 2 the most wanted. */
  priority: number
  quantity: number
  group_gift: boolean
  position: number
  archived: boolean
  created_at: string
  updated_at: string
}

export interface ListOverview {
  lists: List[]
}

export interface ListDetail {
  list: List
  wishes: Wish[]
}

export interface ListRequest {
  title: string
  occasion: string
  greeting: string
  event_date: string
  reserve_open?: boolean
}

export interface WishRequest {
  title: string
  description: string
  url: string
  shop: string
  price_cents: number | null
  currency: string
  priority: number
  quantity: number
  group_gift: boolean
}

export type Direction = 'up' | 'down'

/** The fields a form can show an error under. */
export type ListField = 'title' | 'occasion' | 'greeting' | 'event_date'
export type WishField =
  'title' | 'description' | 'url' | 'shop' | 'price' | 'currency' | 'priority' | 'quantity'

/** The stable keys the server puts in an error's details. */
export type FieldErrorCode = 'required' | 'too_long' | 'invalid'
