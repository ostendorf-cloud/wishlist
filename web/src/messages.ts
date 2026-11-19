// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import type { FieldErrorCode, ListField, WishField } from './api/types'

/**
 * Every string the owner sees, in one place, in English. Issue 10 moves this
 * module into the language layer and adds German: no view holds a literal text.
 * Functions are the strings with a number or a name in them.
 */
export const messages = {
  app: {
    name: 'Wishlist',
    home: 'Your lists',
    skip: 'Skip to content',
    theme: {
      label: 'Color mode',
      light: 'Light',
      dark: 'Dark',
      system: 'System',
    },
  },

  common: {
    save: 'Save',
    saving: 'Saving',
    cancel: 'Cancel',
    delete: 'Delete',
    edit: 'Edit',
    back: 'Back',
    loading: 'Loading',
    archive: 'Archive',
    unarchive: 'Unarchive',
    retry: 'Try again',
    optional: 'optional',
  },

  errors: {
    loadTitle: 'This could not be loaded',
    signedOutTitle: 'You are not signed in',
    signedOut: 'Sign in to see your lists.',
    notFoundTitle: 'Not found',
    notFound: 'This list or wish does not exist, or it is not yours.',
    generic: 'Something went wrong. Please try again.',
    saveFailed: 'The changes were not saved',
    actionFailed: 'That did not work',
  },

  dashboard: {
    title: 'Your lists',
    lead: 'One list per occasion.',
    newList: 'New list',
    showArchived: 'Show archived lists',
    emptyTitle: 'No lists yet',
    emptyText: 'Create a list for a birthday, a wedding or any other occasion.',
    archivedBadge: 'Archived',
    sharedBadge: 'Shared',
    wishCount: (n: number) => (n === 1 ? '1 wish' : `${n} wishes`),
    on: (date: string) => `On ${date}`,
    openList: (title: string) => `Open ${title}`,
  },

  list: {
    addWish: 'Add wish',
    editList: 'Edit list',
    archiveList: 'Archive list',
    unarchiveList: 'Unarchive list',
    deleteList: 'Delete list',
    showArchived: 'Show archived wishes',
    emptyTitle: 'No wishes yet',
    emptyText: 'Add the first wish to this list.',
    archivedBadge: 'Archived list',
    wishesHeading: 'Wishes',
    deleteTitle: 'Delete this list?',
    deleteText: 'The list and all of its wishes are removed for good.',
    deleted: 'List deleted',
    archived: 'List archived',
    unarchived: 'List unarchived',
    guestsClosed: 'Guests cannot take wishes from this list.',
  },

  wish: {
    archivedBadge: 'Archived',
    groupGift: 'Group gift',
    mostWanted: 'Most wanted',
    idea: 'Idea',
    quantity: (n: number) => `Wanted: ${n}`,
    visit: 'Open the link',
    moveUp: 'Move up',
    moveDown: 'Move down',
    moveToList: 'Move to another list',
    moveTitle: 'Move to another list',
    moveText: 'The wish goes to the end of the list you pick.',
    moveTarget: 'List',
    moveConfirm: 'Move',
    moveNoLists: 'You have no other list to move this wish to.',
    moved: 'Wish moved',
    archive: 'Archive',
    unarchive: 'Unarchive',
    archived: 'Wish archived',
    unarchived: 'Wish unarchived',
    deleteTitle: 'Delete this wish?',
    deleteText: 'The wish is removed for good.',
    deleted: 'Wish deleted',
    // The accessible name of a button on a wish: its visible text first, so the
    // name contains what is on screen, then the wish it acts on.
    forWish: (action: string, title: string) => `${action}: ${title}`,
  },

  listForm: {
    newTitle: 'New list',
    editTitle: 'Edit list',
    created: 'List created',
    saved: 'List saved',
    reserveOpen: 'Guests can take wishes from this list',
    reserveOpenHint: 'Turn this off to close the list for guests.',
  },

  wishForm: {
    newTitle: 'New wish',
    editTitle: 'Edit wish',
    created: 'Wish added',
    saved: 'Wish saved',
    groupGift: 'Group gift',
    groupGiftHint: 'Several guests can chip in for this wish.',
    priorityIdea: 'An idea',
    priorityNormal: 'Normal',
    priorityMost: 'Most wanted',
  },

  fields: {
    title: 'Title',
    occasion: 'Occasion',
    greeting: 'Greeting',
    greetingHint: 'Shown to guests above the list.',
    event_date: 'Date',
    eventDateHint: 'Format: 2030-05-01',
    description: 'Description',
    url: 'Link',
    urlHint: 'A web address starting with http:// or https://',
    shop: 'Shop',
    price: 'Price',
    priceHint: 'For example 19.90',
    currency: 'Currency',
    currencyHint: 'Three letters, for example EUR',
    priority: 'Priority',
    quantity: 'How many',
  } satisfies Record<
    | ListField
    | WishField
    | 'greetingHint'
    | 'eventDateHint'
    | 'priceHint'
    | 'currencyHint'
    | 'urlHint',
    string
  >,

  fieldErrors: {
    required: 'This field is required.',
    too_long: 'This text is too long.',
    invalid: 'This value is not valid.',
  } satisfies Record<FieldErrorCode, string>,
} as const

/** The words for an error code the server or a client check put under a field. */
export function fieldError(code: string | undefined): string | undefined {
  if (!code) {
    return undefined
  }
  return code in messages.fieldErrors
    ? messages.fieldErrors[code as FieldErrorCode]
    : messages.errors.generic
}
