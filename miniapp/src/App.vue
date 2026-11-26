<script setup lang="ts">
import { onLaunch, onShow } from '@dcloudio/uni-app'
import { hasSession, logout, refreshSession } from '@/api/http'
import { privacyAgreed } from '@/utils/privacy'
import { initDownloads, wakeDownloads } from '@/utils/offline'

onShow(wakeDownloads)
onLaunch(() => {
  initDownloads()
  // A device that has logged in before renews its token; any other starts at the login page,
  // and so does one whose consent to the privacy policy is missing or for an older version.
  if (hasSession() && privacyAgreed()) refreshSession().catch(() => {})
  else logout()
})
</script>

<style lang="scss">
page {
  // Shared palette: warm paper background, one accent colour.
  --brand: #c0392b;
  --paper: #f6f3ee;
  --line: #efeae2;
  // wot-design-uni takes its accent from this variable.
  --wot-color-theme: #c0392b;
  background: var(--paper);
  color: #2b2622;
  font-size: 28rpx;
  font-family: -apple-system, 'PingFang SC', 'Helvetica Neue', 'Microsoft YaHei', sans-serif;
}

// No scrollbars anywhere: the pages scroll by touch. scroll-view needs `enhanced` and
// `:show-scrollbar="false"` as well, this covers the rest and the browsers.
::-webkit-scrollbar {
  display: none;
  width: 0;
  height: 0;
  color: transparent;
}

.link {
  color: var(--brand);
}

.muted {
  color: #8a8a8a;
  font-size: 24rpx;
}

.ellipsis {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.empty {
  padding: 160rpx 40rpx;
  text-align: center;
  color: #8a8a8a;
}
</style>
