// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { ApiError, ErrorCode, isApiError } from '@ostendorf-cloud/govue'

import { messages } from './messages'

/** Cents as a plain decimal for an input: 1990 becomes "19.90". */
export function centsToInput(cents: number | null): string {
  if (cents === null) {
    return ''
  }
  return `${Math.floor(cents / 100)}.${String(cents % 100).padStart(2, '0')}`
}

/**
 * What a person typed as an amount, in cents. Both "19.90" and "19,90" work.
 * Returns undefined for text that is not an amount and null for an empty field.
 */
export function inputToCents(raw: string): number | null | undefined {
  const text = raw.trim().replace(',', '.')
  if (text === '') {
    return null
  }
  if (!/^\d{1,9}(\.\d{1,2})?$/.test(text)) {
    return undefined
  }
  const [whole, fraction = ''] = text.split('.')
  return Number(whole) * 100 + Number(fraction.padEnd(2, '0'))
}

/** An amount for display, in the currency of the wish. */
export function formatPrice(cents: number, currency: string): string {
  try {
    return new Intl.NumberFormat('en', { style: 'currency', currency }).format(cents / 100)
  } catch {
    return `${centsToInput(cents)} ${currency}`
  }
}

/** A YYYY-MM-DD date for display. */
export function formatDate(day: string): string {
  const parsed = new Date(`${day}T00:00:00Z`)
  if (Number.isNaN(parsed.getTime())) {
    return day
  }
  return new Intl.DateTimeFormat('en', { dateStyle: 'long', timeZone: 'UTC' }).format(parsed)
}

export interface Problem {
  title: string
  message: string
  requestId: string
  signedOut: boolean
  notFound: boolean
}

/** One shape for whatever a call threw, so a view never shows a raw error. */
export function describeError(error: unknown): Problem {
  if (isApiError(error)) {
    const signedOut = error.code === ErrorCode.unauthenticated
    const notFound = error.code === ErrorCode.notFound
    return {
      title: signedOut
        ? messages.errors.signedOutTitle
        : notFound
          ? messages.errors.notFoundTitle
          : messages.errors.loadTitle,
      message: signedOut
        ? messages.errors.signedOut
        : notFound
          ? messages.errors.notFound
          : messages.errors.generic,
      requestId: error.requestId,
      signedOut,
      notFound,
    }
  }
  return {
    title: messages.errors.loadTitle,
    message: messages.errors.generic,
    requestId: '',
    signedOut: false,
    notFound: false,
  }
}

/** The fields of a server refusal under their form names: price_cents is price. */
export function renameDetails(error: unknown, renames: Record<string, string>): unknown {
  if (!(error instanceof ApiError)) {
    return error
  }
  const details: Record<string, string> = {}
  for (const [field, code] of Object.entries(error.details)) {
    details[renames[field] ?? field] = code
  }
  return new ApiError({
    code: error.code,
    message: error.message,
    status: error.status,
    details,
    requestId: error.requestId,
    cause: error,
  })
}
