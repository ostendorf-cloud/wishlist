// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { ErrorCode } from '@ostendorf-cloud/govue'
import { screen, waitFor } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { aList, aWish, apiError, fakeApi, renderApp } from '../testing'

describe('the list form', () => {
  it('refuses an empty title without calling the API', async () => {
    const api = fakeApi()
    await renderApp(api, '/lists/new')

    await userEvent.click(await screen.findByRole('button', { name: 'Save' }))

    expect(await screen.findByText('This field is required.')).toBeVisible()
    expect(screen.getByRole('textbox', { name: /Title/ })).toBeInvalid()
    expect(api.createList).not.toHaveBeenCalled()
  })

  it('creates a list from what was typed, with no owner in the body', async () => {
    const api = fakeApi()
    const { router } = await renderApp(api, '/lists/new')

    await userEvent.type(await screen.findByRole('textbox', { name: /Title/ }), 'Wedding')
    await userEvent.type(screen.getByRole('textbox', { name: 'Date' }), '2030-06-01')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() =>
      expect(api.createList).toHaveBeenCalledWith({
        title: 'Wedding',
        occasion: '',
        greeting: '',
        event_date: '2030-06-01',
        reserve_open: true,
      }),
    )
    await waitFor(() => expect(router.currentRoute.value.fullPath).toBe('/lists/5'))
    expect(await screen.findByText('List created')).toBeVisible()
  })

  it('checks the date format before sending', async () => {
    const api = fakeApi()
    await renderApp(api, '/lists/new')

    await userEvent.type(await screen.findByRole('textbox', { name: /Title/ }), 'x')
    await userEvent.type(screen.getByRole('textbox', { name: 'Date' }), '01.06.2030')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByText('This value is not valid.')).toBeVisible()
    expect(api.createList).not.toHaveBeenCalled()
  })

  it('shows what the server refused under the field', async () => {
    const api = fakeApi({
      createList: () =>
        Promise.reject(apiError(ErrorCode.invalidArgument, 400, { title: 'too_long' })),
    })
    await renderApp(api, '/lists/new')

    await userEvent.type(await screen.findByRole('textbox', { name: /Title/ }), 'x')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByText('This text is too long.')).toBeVisible()
    expect(screen.getByRole('textbox', { name: /Title/ })).toHaveValue('x')
  })

  it('loads a list into the form and saves the change', async () => {
    const api = fakeApi({
      list: () =>
        Promise.resolve({ list: aList({ title: 'Birthday', reserve_open: false }), wishes: [] }),
    })
    await renderApp(api, '/lists/1/edit')

    const title = await screen.findByRole('textbox', { name: /Title/ })
    await waitFor(() => expect(title).toHaveValue('Birthday'))
    expect(screen.getByRole('checkbox', { name: /Guests can take wishes/ })).not.toBeChecked()
    await userEvent.type(title, ' 2')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() =>
      expect(api.updateList).toHaveBeenCalledWith(
        1,
        expect.objectContaining({ title: 'Birthday 2', reserve_open: false }),
      ),
    )
  })
})

describe('the wish form', () => {
  it('sends a price typed with a comma as cents', async () => {
    const api = fakeApi()
    await renderApp(api, '/lists/1/wishes/new')

    await userEvent.type(await screen.findByRole('textbox', { name: /Title/ }), 'Book')
    await userEvent.type(screen.getByRole('textbox', { name: /Price/ }), '19,90')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() =>
      expect(api.createWish).toHaveBeenCalledWith(1, {
        title: 'Book',
        description: '',
        url: '',
        shop: '',
        price_cents: 1990,
        currency: 'EUR',
        priority: 1,
        quantity: 1,
        group_gift: false,
      }),
    )
  })

  it('refuses a link that is not http or https, a bad price and a bad quantity', async () => {
    const api = fakeApi()
    await renderApp(api, '/lists/1/wishes/new')

    await userEvent.type(await screen.findByRole('textbox', { name: /Title/ }), 'Book')
    await userEvent.type(screen.getByRole('textbox', { name: /Link/ }), 'javascript:alert(1)')
    await userEvent.type(screen.getByRole('textbox', { name: /Price/ }), 'free')
    await userEvent.clear(screen.getByRole('textbox', { name: /How many/ }))
    await userEvent.type(screen.getByRole('textbox', { name: /How many/ }), '0')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findAllByText('This value is not valid.')).toHaveLength(3)
    expect(api.createWish).not.toHaveBeenCalled()
  })

  it('shows a refused price under the price field, not under price_cents', async () => {
    const api = fakeApi({
      createWish: () =>
        Promise.reject(apiError(ErrorCode.invalidArgument, 400, { price_cents: 'invalid' })),
    })
    await renderApp(api, '/lists/1/wishes/new')

    await userEvent.type(await screen.findByRole('textbox', { name: /Title/ }), 'Book')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByText('This value is not valid.')).toBeVisible()
    expect(screen.getByRole('textbox', { name: /Price/ })).toBeInvalid()
  })

  it('loads a wish into the form and saves the change on its list', async () => {
    const api = fakeApi({
      wish: () =>
        Promise.resolve(
          aWish({ id: 10, list_id: 3, title: 'Lamp', price_cents: 5, currency: 'USD' }),
        ),
    })
    const { router } = await renderApp(api, '/wishes/10/edit')

    const title = await screen.findByRole('textbox', { name: /Title/ })
    await waitFor(() => expect(title).toHaveValue('Lamp'))
    expect(screen.getByRole('textbox', { name: /Price/ })).toHaveValue('0.05')
    expect(screen.getByRole('textbox', { name: /Currency/ })).toHaveValue('USD')
    await userEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() =>
      expect(api.updateWish).toHaveBeenCalledWith(
        10,
        expect.objectContaining({ title: 'Lamp', price_cents: 5 }),
      ),
    )
    await waitFor(() => expect(router.currentRoute.value.fullPath).toBe('/lists/1'))
  })

  it('says so when the wish to edit is not there', async () => {
    const api = fakeApi({
      wish: () => Promise.reject(apiError(ErrorCode.notFound, 404)),
    })
    await renderApp(api, '/wishes/99/edit')

    expect(await screen.findByText('Not found')).toBeVisible()
  })
})
