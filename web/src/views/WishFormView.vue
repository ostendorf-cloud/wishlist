<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import {
  GovueButton,
  GovueCard,
  GovueCheckbox,
  GovueErrorState,
  GovueField,
  GovueInput,
  GovuePage,
  GovueSelect,
  GovueSkeleton,
  GovueTextarea,
  useForm,
  useToasts,
  type FieldErrors,
} from '@ostendorf-cloud/govue'
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import { useOwnerApi } from '../api/owner'
import type { WishField, WishRequest } from '../api/types'
import { centsToInput, describeError, inputToCents, renameDetails, type Problem } from '../format'
import { fieldError, messages } from '../messages'

// A new wish is added to listId, an existing one is edited by id.
const props = defineProps<{ listId?: string; id?: string }>()

const api = useOwnerApi()
const router = useRouter()
const { notify } = useToasts()

const editing = computed(() => props.id !== undefined)
const loading = ref(editing.value)
const problem = ref<Problem | null>(null)
// The list the wish is on, for the way back.
const listOfWish = ref(props.listId === undefined ? 0 : Number(props.listId))

interface Values {
  title: string
  description: string
  url: string
  shop: string
  price: string
  currency: string
  priority: string
  quantity: string
  group_gift: boolean
}

const priorityOptions = [
  { value: '0', label: messages.wishForm.priorityIdea },
  { value: '1', label: messages.wishForm.priorityNormal },
  { value: '2', label: messages.wishForm.priorityMost },
]

const maxPriceCents = 1_000_000_000

function isWebLink(raw: string): boolean {
  try {
    const url = new URL(raw)
    return (url.protocol === 'http:' || url.protocol === 'https:') && url.host !== ''
  } catch {
    return false
  }
}

// The same limits as internal/webapp/owner/validate.go; the server checks again.
function validate(values: Values): FieldErrors<Values> {
  const errors: FieldErrors<Values> = {}
  const title = values.title.trim()
  if (title === '') {
    errors.title = 'required'
  } else if ([...title].length > 300) {
    errors.title = 'too_long'
  }
  if ([...values.description.trim()].length > 4000) {
    errors.description = 'too_long'
  }
  if ([...values.shop.trim()].length > 200) {
    errors.shop = 'too_long'
  }
  const url = values.url.trim()
  if (url.length > 2000) {
    errors.url = 'too_long'
  } else if (url !== '' && !isWebLink(url)) {
    errors.url = 'invalid'
  }
  const cents = inputToCents(values.price)
  if (cents === undefined || (cents !== null && cents > maxPriceCents)) {
    errors.price = 'invalid'
  }
  const currency = values.currency.trim()
  if (currency !== '' && !/^[A-Za-z]{3}$/.test(currency)) {
    errors.currency = 'invalid'
  }
  const quantity = Number(values.quantity)
  if (!Number.isInteger(quantity) || quantity < 1 || quantity > 99) {
    errors.quantity = 'invalid'
  }
  return errors
}

function request(values: Values): WishRequest {
  return {
    title: values.title,
    description: values.description,
    url: values.url,
    shop: values.shop,
    price_cents: inputToCents(values.price) ?? null,
    currency: values.currency,
    priority: Number(values.priority),
    quantity: Number(values.quantity),
    group_gift: values.group_gift,
  }
}

const form = useForm<Values>({
  initial: {
    title: '',
    description: '',
    url: '',
    shop: '',
    price: '',
    currency: 'EUR',
    priority: '1',
    quantity: '1',
    group_gift: false,
  },
  validate,
  submit: async (values) => {
    try {
      if (props.id !== undefined) {
        const saved = await api.updateWish(Number(props.id), request(values))
        notify({ kind: 'success', title: messages.wishForm.saved })
        await router.push({ name: 'list', params: { id: saved.list_id } })
        return
      }
      const created = await api.createWish(Number(props.listId), request(values))
      notify({ kind: 'success', title: messages.wishForm.created })
      await router.push({ name: 'list', params: { id: created.list_id } })
    } catch (error) {
      // The server names the amount price_cents, the form calls it price.
      throw renameDetails(error, { price_cents: 'price' })
    }
  },
})

async function load(): Promise<void> {
  if (props.id === undefined) {
    return
  }
  loading.value = true
  problem.value = null
  try {
    const wish = await api.wish(Number(props.id))
    listOfWish.value = wish.list_id
    Object.assign(form.values, {
      title: wish.title,
      description: wish.description,
      url: wish.url,
      shop: wish.shop,
      price: centsToInput(wish.price_cents),
      currency: wish.currency,
      priority: String(wish.priority),
      quantity: String(wish.quantity),
      group_gift: wish.group_gift,
    })
  } catch (error) {
    problem.value = describeError(error)
  } finally {
    loading.value = false
  }
}

onMounted(load)

function errorOf(field: WishField): string | undefined {
  return fieldError(form.errorOf(field))
}

function cancel(): void {
  void (listOfWish.value > 0
    ? router.push({ name: 'list', params: { id: listOfWish.value } })
    : router.push({ name: 'dashboard' }))
}
</script>

<template>
  <GovuePage :title="editing ? messages.wishForm.editTitle : messages.wishForm.newTitle">
    <div v-if="loading" role="status" :aria-label="messages.common.loading">
      <GovueSkeleton :lines="6" />
    </div>

    <GovueErrorState
      v-else-if="problem"
      :title="problem.title"
      :message="problem.message"
      :request-id="problem.requestId"
      :retry-label="messages.common.retry"
      @retry="load"
    />

    <GovueCard v-else>
      <form class="flex flex-col gap-4" novalidate @submit.prevent="form.handleSubmit()">
        <GovueField :label="messages.fields.title" :error="errorOf('title')" required>
          <GovueInput v-model="form.values.title" @blur="form.blur('title')" />
        </GovueField>

        <GovueField :label="messages.fields.description" :error="errorOf('description')">
          <GovueTextarea v-model="form.values.description" @blur="form.blur('description')" />
        </GovueField>

        <GovueField
          :label="messages.fields.url"
          :hint="messages.fields.urlHint"
          :error="errorOf('url')"
        >
          <GovueInput v-model="form.values.url" type="url" @blur="form.blur('url')" />
        </GovueField>

        <GovueField :label="messages.fields.shop" :error="errorOf('shop')">
          <GovueInput v-model="form.values.shop" @blur="form.blur('shop')" />
        </GovueField>

        <div class="grid gap-4 sm:grid-cols-2">
          <GovueField
            :label="messages.fields.price"
            :hint="messages.fields.priceHint"
            :error="errorOf('price')"
          >
            <GovueInput
              v-model="form.values.price"
              inputmode="decimal"
              @blur="form.blur('price')"
            />
          </GovueField>

          <GovueField
            :label="messages.fields.currency"
            :hint="messages.fields.currencyHint"
            :error="errorOf('currency')"
          >
            <GovueInput v-model="form.values.currency" @blur="form.blur('currency')" />
          </GovueField>

          <GovueField :label="messages.fields.priority" :error="errorOf('priority')">
            <GovueSelect v-model="form.values.priority" :options="priorityOptions" />
          </GovueField>

          <GovueField :label="messages.fields.quantity" :error="errorOf('quantity')">
            <GovueInput
              v-model="form.values.quantity"
              inputmode="numeric"
              @blur="form.blur('quantity')"
            />
          </GovueField>
        </div>

        <GovueCheckbox
          v-model="form.values.group_gift"
          :label="messages.wishForm.groupGift"
          :hint="messages.wishForm.groupGiftHint"
        />

        <p v-if="form.problem.value" role="alert" class="text-danger-text">
          {{ messages.errors.saveFailed }}: {{ form.problem.value }}
          <span v-if="form.requestId.value" class="text-text-muted">
            ({{ form.requestId.value }})
          </span>
        </p>

        <div class="flex flex-wrap gap-2">
          <GovueButton
            type="submit"
            variant="primary"
            :loading="form.submitting.value"
            :loading-label="messages.common.saving"
          >
            {{ messages.common.save }}
          </GovueButton>
          <GovueButton @click="cancel">{{ messages.common.cancel }}</GovueButton>
        </div>
      </form>
    </GovueCard>
  </GovuePage>
</template>
