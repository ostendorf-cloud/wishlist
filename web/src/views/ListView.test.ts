// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { screen, waitFor, within } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import { aList, aWish, fakeApi, notFound, renderApp } from '../testing'

const two = () =>
  Promise.resolve({
    list: aList({ greeting: 'Hello guests' }),
    wishes: [
      aWish({
        id: 10,
        title: 'Book',
        price_cents: 1990,
        shop: 'Shop',
        url: 'https://a.example.invalid/x',
      }),
      aWish({ id: 20, title: 'Lamp', priority: 2, quantity: 2, group_gift: true, position: 1 }),
    ],
  })

describe('ListView', () => {
  it('shows the list and its wishes', async () => {
    await renderApp(fakeApi({ list: two }), '/lists/1')

    expect(await screen.findByRole('heading', { name: 'Book' })).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Birthday', level: 1 })).toBeVisible()
    expect(screen.getByText('Hello guests')).toBeVisible()
    expect(screen.getByText('Shop · €19.90')).toBeVisible()
    expect(screen.getByText('Most wanted')).toBeVisible()
    expect(screen.getByText('Wanted: 2')).toBeVisible()
    expect(screen.getByText('Group gift')).toBeVisible()
    const link = screen.getByRole('link', { name: 'Open the link' })
    expect(link).toHaveAttribute('href', 'https://a.example.invalid/x')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  })

  it('shows an empty state with the way to add a wish', async () => {
    const { router } = await renderApp(
      fakeApi({ list: () => Promise.resolve({ list: aList(), wishes: [] }) }),
      '/lists/1',
    )
    expect(await screen.findByText('No wishes yet')).toBeVisible()

    const buttons = screen.getAllByRole('button', { name: 'Add wish' })
    await userEvent.click(buttons[buttons.length - 1])
    await waitFor(() => expect(router.currentRoute.value.fullPath).toBe('/lists/1/wishes/new'))
  })

  it('says a list that is not there is not found, whoever it belongs to', async () => {
    const { router } = await renderApp(
      fakeApi({
        list: () => Promise.reject(notFound()),
      }),
      '/lists/9',
    )
    expect(await screen.findByText('Not found')).toBeVisible()
    await userEvent.click(screen.getByRole('button', { name: 'Back' }))
    await waitFor(() => expect(router.currentRoute.value.fullPath).toBe('/'))
  })

  it('asks for archived wishes only when the filter is on', async () => {
    const api = fakeApi({ list: two })
    await renderApp(api, '/lists/1')
    await screen.findByRole('heading', { name: 'Book' })
    expect(api.list).toHaveBeenLastCalledWith(1, { archived: false })

    await userEvent.click(screen.getByRole('checkbox', { name: 'Show archived wishes' }))

    await waitFor(() => expect(api.list).toHaveBeenLastCalledWith(1, { archived: true }))
  })

  it('moves a wish up and down, and cannot move the first one up', async () => {
    const api = fakeApi({ list: two })
    await renderApp(api, '/lists/1')
    await screen.findByRole('heading', { name: 'Book' })

    expect(screen.getByRole('button', { name: 'Move up: Book' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Move down: Lamp' })).toBeDisabled()

    await userEvent.click(screen.getByRole('button', { name: 'Move up: Lamp' }))
    await waitFor(() => expect(api.moveWish).toHaveBeenCalledWith(20, 'up'))
    await userEvent.click(screen.getByRole('button', { name: 'Move down: Book' }))
    await waitFor(() => expect(api.moveWish).toHaveBeenCalledWith(10, 'down'))
  })

  it('archives and unarchives a wish', async () => {
    const api = fakeApi({
      list: () =>
        Promise.resolve({
          list: aList(),
          wishes: [aWish({ id: 10 }), aWish({ id: 30, title: 'Old', archived: true, position: 1 })],
        }),
    })
    await renderApp(api, '/lists/1')
    await screen.findByRole('heading', { name: 'Book' })

    await userEvent.click(screen.getByRole('button', { name: 'Archive: Book' }))
    await waitFor(() => expect(api.archiveWish).toHaveBeenCalledWith(10, true))
    await userEvent.click(screen.getByRole('button', { name: 'Unarchive: Old' }))
    await waitFor(() => expect(api.archiveWish).toHaveBeenCalledWith(30, false))
  })

  it('deletes a wish only after the dialog is confirmed', async () => {
    const api = fakeApi({ list: two })
    await renderApp(api, '/lists/1')
    await screen.findByRole('heading', { name: 'Book' })

    await userEvent.click(screen.getByRole('button', { name: 'Delete: Book' }))
    let dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Delete this wish?')).toBeVisible()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(api.deleteWish).not.toHaveBeenCalled()

    await userEvent.click(screen.getByRole('button', { name: 'Delete: Book' }))
    dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(api.deleteWish).toHaveBeenCalledWith(10))
  })

  it('deletes a list after confirming and goes back to the dashboard', async () => {
    const api = fakeApi({ list: two })
    const { router } = await renderApp(api, '/lists/1')
    await screen.findByRole('heading', { name: 'Book' })

    await userEvent.click(screen.getByRole('button', { name: 'Delete list' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('Delete this list?')).toBeVisible()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))

    await waitFor(() => expect(api.deleteList).toHaveBeenCalledWith(1))
    await waitFor(() => expect(router.currentRoute.value.fullPath).toBe('/'))
  })

  it('archives the list', async () => {
    const api = fakeApi({ list: two })
    await renderApp(api, '/lists/1')
    await screen.findByRole('heading', { name: 'Book' })

    await userEvent.click(screen.getByRole('button', { name: 'Archive list' }))

    await waitFor(() => expect(api.archiveList).toHaveBeenCalledWith(1, true))
  })

  it('moves a wish to another list', async () => {
    const api = fakeApi({
      list: two,
      lists: () =>
        Promise.resolve({ lists: [aList({ id: 1 }), aList({ id: 2, title: 'Christmas' })] }),
    })
    await renderApp(api, '/lists/1')
    await screen.findByRole('heading', { name: 'Book' })

    await userEvent.click(screen.getByRole('button', { name: 'Move to another list: Book' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Move' }))

    await waitFor(() => expect(api.relocateWish).toHaveBeenCalledWith(10, 2))
  })

  it('says there is nowhere to move a wish when there is one list only', async () => {
    const api = fakeApi({ list: two, lists: () => Promise.resolve({ lists: [aList({ id: 1 })] }) })
    await renderApp(api, '/lists/1')
    await screen.findByRole('heading', { name: 'Book' })

    await userEvent.click(screen.getByRole('button', { name: 'Move to another list: Book' }))
    const dialog = await screen.findByRole('dialog')

    expect(within(dialog).getByText('You have no other list to move this wish to.')).toBeVisible()
    expect(within(dialog).getByRole('button', { name: 'Move' })).toBeDisabled()
  })

  it('tells the owner when an action fails and keeps the page', async () => {
    const api = fakeApi({
      list: two,
      deleteWish: () => Promise.reject(notFound()),
    })
    await renderApp(api, '/lists/1')
    await screen.findByRole('heading', { name: 'Book' })

    await userEvent.click(screen.getByRole('button', { name: 'Delete: Book' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Delete' }))

    expect(await screen.findByText('That did not work')).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Book' })).toBeVisible()
  })
})
