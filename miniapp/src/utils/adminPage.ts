import { computed, ref } from 'vue'
import { onHide, onShow, onUnload } from '@dcloudio/uni-app'
import { errorText, isAdmin, refreshAccount } from '@/api/http'

/** Recheck permissions on direct entry and when returning to a privileged page. */
export function useAdminPage() {
  const checked = ref(false)
  const loading = ref(true)
  const error = ref('')
  const allowed = computed(() => checked.value && isAdmin.value)
  let revision = 0

  async function refresh() {
    const current = ++revision
    checked.value = false
    loading.value = true
    error.value = ''
    try {
      await refreshAccount()
      if (current === revision) checked.value = true
    } catch (e) {
      if (current === revision) error.value = errorText(e)
    } finally {
      if (current === revision) loading.value = false
    }
  }

  function leave() {
    revision++
    checked.value = false
  }

  onShow(refresh)
  onHide(leave)
  onUnload(leave)
  return { allowed, loading, error, refresh }
}
