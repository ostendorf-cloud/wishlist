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
  GovueSkeleton,
  GovueTextarea,
  useForm,
  useToasts,
  type FieldErrors,
} from '@ostendorf-cloud/govue'
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import { useOwnerApi } from '../api/owner'
import type { ListField } from '../api/types'
import { describeError, type Problem } from '../format'
import { fieldError, messages } from '../messages'

const props = defineProps<{ id?: string }>()

const api = useOwnerApi()
const router = useRouter()
const { notify } = useToasts()

const editing = computed(() => props.id !== undefined)
const loading = ref(editing.value)
const problem = ref<Problem | null>(null)

interface Values {
  title: string
  occasion: string
  greeting: string
  event_date: string
  reserve_open: boolean
}

// The same limits as internal/webapp/owner/validate.go. The server checks them
// again: these only save a round trip.
function validate(values: Values): FieldErrors<Values> {
  const errors: FieldErrors<Values> = {}
  const title = values.title.trim()
  if (title === '') {
    errors.title = 'required'
  } else if ([...title].length > 200) {
    errors.title = 'too_long'
  }
  if ([...values.occasion.trim()].length > 200) {
    errors.occasion = 'too_long'
  }
  if ([...values.greeting.trim()].length > 2000) {
    errors.greeting = 'too_long'
  }
  const date = values.event_date.trim()
  if (date !== '' && !/^\d{4}-\d{2}-\d{2}$/.test(date)) {
    errors.event_date = 'invalid'
  }
  return errors
}

const form = useForm<Values>({
  initial: { title: '', occasion: '', greeting: '', event_date: '', reserve_open: true },
  validate,
  submit: async (values) => {
    const body = {
      title: values.title,
      occasion: values.occasion,
      greeting: values.greeting,
      event_date: values.event_date,
      reserve_open: values.reserve_open,
    }
    if (props.id !== undefined) {
      const saved = await api.updateList(Number(props.id), body)
      notify({ kind: 'success', title: messages.listForm.saved })
      await router.push({ name: 'list', params: { id: saved.id } })
      return
    }
    const created = await api.createList(body)
    notify({ kind: 'success', title: messages.listForm.created })
    await router.push({ name: 'list', params: { id: created.id } })
  },
})

async function load(): Promise<void> {
  if (props.id === undefined) {
    return
  }
  loading.value = true
  problem.value = null
  try {
    const { list } = await api.list(Number(props.id))
    Object.assign(form.values, {
      title: list.title,
      occasion: list.occasion,
      greeting: list.greeting,
      event_date: list.event_date ?? '',
      reserve_open: list.reserve_open,
    })
  } catch (error) {
    problem.value = describeError(error)
  } finally {
    loading.value = false
  }
}

onMounted(load)

function errorOf(field: ListField): string | undefined {
  return fieldError(form.errorOf(field))
}

function cancel(): void {
  void (props.id === undefined
    ? router.push({ name: 'dashboard' })
    : router.push({ name: 'list', params: { id: props.id } }))
}
</script>

<template>
  <GovuePage :title="editing ? messages.listForm.editTitle : messages.listForm.newTitle">
    <div v-if="loading" role="status" :aria-label="messages.common.loading">
      <GovueSkeleton :lines="5" />
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

        <GovueField :label="messages.fields.occasion" :error="errorOf('occasion')">
          <GovueInput v-model="form.values.occasion" @blur="form.blur('occasion')" />
        </GovueField>

        <GovueField
          :label="messages.fields.event_date"
          :hint="messages.fields.eventDateHint"
          :error="errorOf('event_date')"
        >
          <GovueInput v-model="form.values.event_date" @blur="form.blur('event_date')" />
        </GovueField>

        <GovueField
          :label="messages.fields.greeting"
          :hint="messages.fields.greetingHint"
          :error="errorOf('greeting')"
        >
          <GovueTextarea v-model="form.values.greeting" @blur="form.blur('greeting')" />
        </GovueField>

        <GovueCheckbox
          v-model="form.values.reserve_open"
          :label="messages.listForm.reserveOpen"
          :hint="messages.listForm.reserveOpenHint"
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
