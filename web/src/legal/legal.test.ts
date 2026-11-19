// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { screen, within } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'

import { routes } from '../router'
import { fakeApi, renderApp } from '../testing'
import { legalRouteRecords, pages } from './site'

const paths = ['/de/imprint', '/en/imprint', '/de/privacy', '/en/privacy']

describe('legal pages', () => {
  it('has the four canonical routes and no terms route', () => {
    expect(legalRouteRecords.map((r) => r.path).sort()).toEqual([...paths].sort())
    expect(pages).not.toHaveProperty('terms')
    expect(routes.some((r) => r.path.includes('terms') || r.path.includes('agb'))).toBe(false)
  })

  it.each([
    ['/de/imprint', 'Impressum'],
    ['/en/imprint', 'Imprint'],
    ['/de/privacy', 'Datenschutzerklärung'],
    ['/en/privacy', 'Privacy policy'],
  ])('renders %s with the heading %s and one main landmark', async (path, heading) => {
    await renderApp(fakeApi(), path)

    expect(await screen.findByRole('heading', { name: heading, level: 1 })).toBeVisible()
    expect(screen.getAllByRole('main')).toHaveLength(1)
  })

  it.each([
    ['/de/imprint', 'Kontakt', 'Umsatzsteuer-ID'],
    ['/en/imprint', 'Contact', 'VAT ID'],
  ])('states the operator on %s', async (path, contact, vat) => {
    await renderApp(fakeApi(), path)
    const main = within(await screen.findByRole('main'))

    expect(main.getByText(/Simon Ostendorf/)).toBeVisible()
    expect(main.getByText(/Frankenstr\. 8/)).toBeVisible()
    expect(main.getByText(/52070 Aachen/)).toBeVisible()
    expect(main.getByRole('heading', { name: contact })).toBeVisible()
    expect(main.getByRole('heading', { name: vat })).toBeVisible()
    expect(main.getByRole('link', { name: '+49 1573 0971784' })).toHaveAttribute(
      'href',
      'tel:+4915730971784',
    )
    expect(main.getByRole('link', { name: 'info@my-wishes.de' })).toHaveAttribute(
      'href',
      'mailto:info@my-wishes.de',
    )
    expect(main.getByText(/DE362164357/)).toBeVisible()
  })

  it.each(paths)('has no dispute resolution section on %s', async (path) => {
    await renderApp(fakeApi(), path)
    const main = await screen.findByRole('main')

    expect(main.textContent).not.toMatch(
      /Streitbeilegung|Verbraucherschlichtung|dispute resolution/i,
    )
  })

  it.each(['/de/privacy', '/en/privacy'])('states only what exists today on %s', async (path) => {
    await renderApp(fakeApi(), path)
    const text = (await screen.findByRole('main')).textContent ?? ''

    expect(text).not.toMatch(/analytics|Reichweite|guest|Gäste/i)
    expect(text).toMatch(/info@my-wishes\.de/)
    expect(text).toMatch(/IP/)
  })

  it.each(['/de/privacy', '/en/privacy'])(
    'names the session cookie and the 24 hours of unconfirmed accounts on %s',
    async (path) => {
      await renderApp(fakeApi(), path)
      const text = (await screen.findByRole('main')).textContent ?? ''

      expect(text).toContain('__Host-session')
      expect(text).toMatch(/\b24\b/)
      expect(text).toMatch(/TOTP/)
      expect(text).toMatch(/Argon2id/)
      expect(text).toMatch(/CSRF/)
      expect(text).not.toMatch(/keine Cookies|Anmeldung ist derzeit nicht möglich|sets no cookies/)
      expect(text).not.toMatch(/Signing in is not possible/)
    },
  )

  it.each([
    ['/de/privacy', /bei der nächsten Anfrage entfernt/, /mindestens einmal pro Minute/],
    ['/en/privacy', /removed at the next request/, /at least once per minute/],
  ])('says the limiter tidies up on a request only, on %s', async (path, yes, no) => {
    await renderApp(fakeApi(), path)
    const text = (await screen.findByRole('main')).textContent ?? ''

    expect(text).toMatch(yes)
    expect(text).not.toMatch(no)
  })

  it.each([
    ['/de/privacy', ['Inhalte Ihres Kontos', 'Anlass', 'Gruppengeschenk|gemeinsames Geschenk']],
    ['/en/privacy', ['Content of your account', 'occasion', 'group gift']],
  ])('lists what an account stores on %s', async (path, [heading, occasion, group]) => {
    await renderApp(fakeApi(), path)
    const main = within(await screen.findByRole('main'))
    const text = main.getByRole('heading', { name: heading }).parentElement?.textContent ?? ''

    expect(text).toMatch(new RegExp(occasion))
    expect(text).toMatch(new RegExp(group))
    expect(text).toMatch(/owner-/)
    for (const word of [
      'Priorität|priority',
      'Preis|price',
      'Geschäft|shop',
      'Internetadresse|web address',
    ]) {
      expect(text).toMatch(new RegExp(word))
    }
  })

  it.each([
    ['/de/privacy', 'Besucherstatistik mit Umami', /Routenmuster/, /Do Not Track/, /\/24/, /\/48/],
    [
      '/en/privacy',
      'Visitor statistics with Umami',
      /route pattern/,
      /Do Not Track/,
      /\/24/,
      /\/48/,
    ],
  ])('states what the page view counting reports on %s', async (path, heading, ...fragments) => {
    await renderApp(fakeApi(), path)
    const element = await screen.findByRole('main')

    expect(within(element).getByRole('heading', { name: heading })).toBeVisible()
    const text = element.textContent ?? ''
    for (const fragment of fragments) {
      expect(text).toMatch(fragment)
    }
    expect(text).toMatch(/Global Privacy Control/)
  })

  it.each(paths)('has a back link to the start page that exists, on %s', async (path) => {
    await renderApp(fakeApi(), path)
    const main = within(await screen.findByRole('main'))
    const back = main.getAllByRole('link')[0]

    expect(back).toHaveAttribute('href', '/')
  })

  it('notes in English that the German text is binding, and not in German', async () => {
    await renderApp(fakeApi(), '/en/privacy')
    expect(await screen.findByText(/German text is the legally binding one/)).toBeVisible()
    expect(screen.getByRole('link', { name: 'View the German version' })).toHaveAttribute(
      'href',
      '/de/privacy',
    )
  })

  it.each([
    ['/de/imprint', 'de'],
    ['/en/privacy', 'en'],
    ['/', 'en'],
    ['/lists/new', 'en'],
  ])('links the legal pages of its own language in the footer on %s', async (path, code) => {
    await renderApp(fakeApi(), path)
    const nav = within(screen.getByRole('contentinfo'))

    const links = nav.getAllByRole('link').map((a) => a.getAttribute('href'))
    expect(links).toEqual([`/${code}/imprint`, `/${code}/privacy`])
  })
})
