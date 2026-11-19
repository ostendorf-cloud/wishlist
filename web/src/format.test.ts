// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'

import { centsToInput, formatDate, formatPrice, inputToCents } from './format'
import { fieldError, messages } from './messages'

describe('amounts', () => {
  it.each([
    ['19.90', 1990],
    ['19,90', 1990],
    ['19', 1900],
    ['0.5', 50],
    [' 7.05 ', 705],
    ['', null],
  ])('reads %j as %j', (typed, cents) => {
    expect(inputToCents(typed)).toBe(cents)
  })

  it.each(['free', '1.999', '-3', '1e3', '1,000.00', '9999999999'])('refuses %j', (typed) => {
    expect(inputToCents(typed)).toBeUndefined()
  })

  it('writes cents back the way they were typed', () => {
    expect(centsToInput(1990)).toBe('19.90')
    expect(centsToInput(5)).toBe('0.05')
    expect(centsToInput(null)).toBe('')
  })

  it('formats a price and a date for display', () => {
    expect(formatPrice(1990, 'EUR')).toBe('€19.90')
    expect(formatPrice(1990, 'nope')).toBe('19.90 nope')
    expect(formatDate('2030-05-01')).toBe('May 1, 2030')
    expect(formatDate('garbage')).toBe('garbage')
  })
})

describe('messages', () => {
  it('words every field error code the server can send', () => {
    for (const code of ['required', 'too_long', 'invalid']) {
      expect(fieldError(code)).toBe(messages.fieldErrors[code as 'required'])
    }
    expect(fieldError(undefined)).toBeUndefined()
    expect(fieldError('something new')).toBe(messages.errors.generic)
  })
})
