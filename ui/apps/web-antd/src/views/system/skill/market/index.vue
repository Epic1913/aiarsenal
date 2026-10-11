<script lang="ts" setup>
import type { SkillMarketApi } from '#/api/system/skill/market';

import { onMounted, reactive, ref } from 'vue';

import { Page } from '@vben/common-ui';

import {
  Button,
  Card,
  Empty,
  Form,
  Input,
  InputNumber,
  message,
  Modal,
  Popconfirm,
  Select,
  Switch,
} from 'ant-design-vue';

import { getAllSkillNamespacesApi } from '#/api/system/skill/namespace';
import {
  addMarketSiteApi,
  deleteMarketSiteApi,
  editMarketSiteApi,
  importMarketUrlApi,
  listMarketSitesApi,
} from '#/api/system/skill/market';
import { $t } from '#/locales';

defineOptions({ name: 'SkillMarket' });

// 外部市场目录（aa_skill_market_site）：只存链接引导用户去原站，平台不代搜不代抓
const sites = ref<SkillMarketApi.Site[]>([]);
const failedLogos = ref<string[]>([]);

function openUrl(url?: string) {
  if (url) window.open(url, '_blank');
}

function onLogoError(key: string) {
  if (!failedLogos.value.includes(key)) {
    failedLogos.value = [...failedLogos.value, key];
  }
}

async function loadSites() {
  try {
    const res = await listMarketSitesApi();
    sites.value = res.list || [];
  } catch (error: any) {
    message.error(error?.message || $t('skill.market_search_fail'));
  }
}

// 链接导入目标命名空间
const namespaces = ref<{
  displayName: string;
  namespaceId: string;
  status: number;
}[]>([]);
const currentNamespace = ref('');

async function loadNamespaces() {
  const res: any = await getAllSkillNamespacesApi();
  namespaces.value = (res?.list || []).filter((n: any) => n.status === 1);
  if (namespaces.value.length > 0 && !currentNamespace.value) {
    currentNamespace.value = namespaces.value[0]!.namespaceId;
  }
}

// ===== 链接导入（外部市场拿到的 zip 直链/详情页链接 → 本地技能库，记原作者/标签溯源） =====
const importUrl = ref('');
const importAuthor = ref('');
const importTags = ref<string[]>([]);
const importing = ref(false);

async function onImport() {
  const url = importUrl.value.trim();
  if (!url) {
    message.warning($t('skill.market_import_empty'));
    return;
  }
  if (!currentNamespace.value) {
    message.warning($t('skill.market_pick_ns'));
    return;
  }
  importing.value = true;
  try {
    await importMarketUrlApi({
      url,
      namespaceId: currentNamespace.value,
      upstreamAuthor: importAuthor.value.trim() || undefined,
      tags: importTags.value,
    });
    message.success($t('skill.market_import_ok'));
    importUrl.value = '';
    importAuthor.value = '';
    importTags.value = [];
  } catch (error: any) {
    message.error(
      `${$t('skill.market_import_fail')}${error?.message ? `: ${error.message}` : ''}`,
    );
  } finally {
    importing.value = false;
  }
}

// ===== 目录增删改（管理弹窗） =====
const modalVisible = ref(false);
const editing = ref(false);
const saving = ref(false);
const formState = reactive({
  marketId: '',
  name: '',
  siteUrl: '',
  description: '',
  icon: '',
  logoUrl: '',
  themeColor: '',
  status: 1,
  sort: 0,
});

function openAdd() {
  editing.value = false;
  Object.assign(formState, {
    marketId: '',
    name: '',
    siteUrl: '',
    description: '',
    icon: '🌐',
    logoUrl: '',
    themeColor: '#42b983',
    status: 1,
    sort: 0,
  });
  modalVisible.value = true;
}

function openEdit(row: SkillMarketApi.Site) {
  editing.value = true;
  Object.assign(formState, {
    marketId: row.marketId,
    name: row.name,
    siteUrl: row.siteUrl,
    description: row.description || '',
    icon: row.icon || '',
    logoUrl: row.logoUrl || '',
    themeColor: row.themeColor || '',
    status: row.status,
    sort: row.sort,
  });
  modalVisible.value = true;
}

async function onSave() {
  if (!formState.name.trim() || !formState.siteUrl.trim()) {
    message.warning($t('skill.market_site_form_required'));
    return;
  }
  saving.value = true;
  try {
    const payload: SkillMarketApi.SiteReq = {
      name: formState.name.trim(),
      siteUrl: formState.siteUrl.trim(),
      description: formState.description.trim(),
      icon: formState.icon.trim(),
      logoUrl: formState.logoUrl.trim(),
      themeColor: formState.themeColor.trim(),
      status: formState.status,
      sort: formState.sort,
    };
    if (editing.value) {
      await editMarketSiteApi({ ...payload, marketId: formState.marketId });
      message.success($t('skill.market_site_edit_ok'));
    } else {
      await addMarketSiteApi(payload);
      message.success($t('skill.market_site_add_ok'));
    }
    modalVisible.value = false;
    await loadSites();
  } catch (error: any) {
    message.error(
      `${editing.value ? $t('skill.market_site_edit_fail') : $t('skill.market_site_add_fail')}${error?.message ? `: ${error.message}` : ''}`,
    );
  } finally {
    saving.value = false;
  }
}

async function onDelete(row: SkillMarketApi.Site) {
  try {
    await deleteMarketSiteApi(row.marketId);
    message.success($t('skill.market_site_del_ok'));
    await loadSites();
  } catch (error: any) {
    message.error(
      `${$t('skill.market_site_del_fail')}${error?.message ? `: ${error.message}` : ''}`,
    );
  }
}

onMounted(() => {
  loadSites();
  loadNamespaces();
});
</script>

<template>
  <Page :auto-content-height="true">
    <div class="space-y-4">
      <!-- 使用引导 -->
      <Card>
        <template #title>
          <span>{{ $t('skill.market_guide_title') }}</span>
        </template>
        <div class="text-sm leading-7">
          {{ $t('skill.market_guide_step1') }}
          <br />
          {{ $t('skill.market_guide_step2') }}
          <br />
          {{ $t('skill.market_guide_step3') }}
        </div>
      </Card>

      <!-- 市场目录（点卡片跳原站；右上角管理） -->
      <Card>
        <template #title>
          <span>{{ $t('skill.market_siteonly_title') }}</span>
        </template>
        <template #extra>
          <Button size="small" @click="openAdd">
            {{ $t('skill.market_site_add') }}
          </Button>
        </template>
        <Empty
          v-if="sites.length === 0"
          :description="$t('skill.market_no_data')"
        />
        <div
          v-else
          class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4"
        >
          <div
            v-for="s in sites"
            :key="s.marketId"
            class="site-card flex cursor-pointer items-center gap-3 rounded-xl border border-solid p-4"
            :style="{ '--c': s.themeColor || '#42b983' }"
            @click="openUrl(s.siteUrl)"
          >
            <span
              class="site-logo flex h-12 min-w-12 shrink-0 items-center justify-center overflow-hidden rounded-xl px-1.5 text-2xl"
              :style="{ background: `color-mix(in srgb, ${s.themeColor || '#42b983'} 12%, #fff)` }"
            >
              <!-- 横版 logo（如腾讯技能社区的横版标识）按高 28px 等比展开，方形图标不受影响 -->
              <img
                v-if="s.logoUrl && !failedLogos.includes(s.marketId)"
                :src="s.logoUrl"
                class="h-7 w-auto max-w-24 object-contain"
                @error="onLogoError(s.marketId)"
              />
              <span v-else>{{ s.icon }}</span>
            </span>
            <div class="min-w-0 flex-1">
              <div
                class="truncate font-semibold"
                :style="{ color: s.themeColor }"
              >
                {{ s.name }}
              </div>
              <div class="truncate text-xs text-gray-500 dark:text-gray-400">
                {{ s.description }}
              </div>
            </div>
            <span
              class="flex shrink-0 items-center gap-1"
              @click.stop
            >
              <Button
                type="text"
                size="small"
                @click.stop="openEdit(s)"
              >
                ✏️
              </Button>
              <Popconfirm
                :title="$t('skill.market_site_del_confirm')"
                @confirm="onDelete(s)"
              >
                <Button type="text" size="small" danger @click.stop>
                  🗑️
                </Button>
              </Popconfirm>
            </span>
          </div>
        </div>
      </Card>

      <!-- 链接导入（外部市场拿到的安装引用 → 本地技能库） -->
      <Card>
        <template #title>
          <span>{{ $t('skill.market_import_title') }}</span>
        </template>
        <template #extra>
          <span class="text-xs text-gray-400">{{
            $t('skill.market_import_hint')
          }}</span>
        </template>
        <div class="flex flex-wrap items-center gap-3">
          <div class="min-w-64 flex-1">
            <Input
              v-model:value="importUrl"
              allow-clear
              :placeholder="$t('skill.market_import_ph')"
              @press-enter="onImport"
            />
          </div>
          <div class="flex items-center gap-2">
            <span class="text-sm text-gray-500">{{
              $t('skill.market_namespace')
            }}</span>
            <Select
              v-model:value="currentNamespace"
              style="width: 180px"
              :placeholder="$t('skill.market_namespace_ph')"
            >
              <Select.Option
                v-for="ns in namespaces"
                :key="ns.namespaceId"
                :value="ns.namespaceId"
              >
                {{ ns.displayName }}
              </Select.Option>
            </Select>
          </div>
          <div class="flex items-center gap-2">
            <span class="text-sm text-gray-500">{{
              $t('skill.market_import_author')
            }}</span>
            <Input
              v-model:value="importAuthor"
              style="width: 160px"
              :placeholder="$t('skill.market_import_author_ph')"
            />
          </div>
          <div class="flex items-center gap-2">
            <span class="text-sm text-gray-500">{{
              $t('skill.market_import_tags')
            }}</span>
            <Select
              v-model:value="importTags"
              mode="tags"
              style="min-width: 180px"
              :open="false"
              :placeholder="$t('skill.market_import_tags_ph')"
            />
          </div>
          <Button type="primary" ghost :loading="importing" @click="onImport">
            {{ $t('skill.market_import_btn') }}
          </Button>
        </div>
      </Card>

      <!-- 新增/编辑弹窗 -->
      <Modal
        v-model:open="modalVisible"
        :title="
          editing
            ? $t('skill.market_site_edit_title')
            : $t('skill.market_site_add_title')
        "
        :confirm-loading="saving"
        @ok="onSave"
      >
        <Form layout="vertical" class="pt-2">
          <Form.Item
            :label="$t('skill.market_site_name')"
            required
          >
            <Input
              v-model:value="formState.name"
              :placeholder="$t('skill.market_site_name_ph')"
              :maxlength="50"
            />
          </Form.Item>
          <Form.Item
            :label="$t('skill.market_site_url')"
            required
          >
            <Input
              v-model:value="formState.siteUrl"
              placeholder="https://…"
            />
          </Form.Item>
          <Form.Item :label="$t('skill.market_site_desc')">
            <Input
              v-model:value="formState.description"
              :maxlength="200"
            />
          </Form.Item>
          <div class="flex gap-3">
            <Form.Item :label="$t('skill.market_site_icon')" class="flex-1">
              <Input
                v-model:value="formState.icon"
                :maxlength="8"
                :placeholder="$t('skill.market_site_icon_ph')"
              />
            </Form.Item>
            <Form.Item :label="$t('skill.market_site_logo')" class="flex-1">
              <Input v-model:value="formState.logoUrl" placeholder="https://…" />
            </Form.Item>
          </div>
          <div class="flex gap-3">
            <Form.Item :label="$t('skill.market_site_theme')" class="flex-1">
              <Input v-model:value="formState.themeColor" placeholder="#42b983" />
            </Form.Item>
            <Form.Item :label="$t('skill.market_site_sort')" class="flex-1">
              <InputNumber
                v-model:value="formState.sort"
                class="w-full"
                :precision="0"
              />
            </Form.Item>
            <Form.Item :label="$t('skill.market_site_status')" class="flex-1">
              <Switch
                :checked="formState.status === 1"
                @change="
                  (v: any) => (formState.status = v ? 1 : 0)
                "
              />
            </Form.Item>
          </div>
        </Form>
      </Modal>
    </div>
  </Page>
</template>

<style scoped>
.site-card {
  --c: #42b983;
  border-color: color-mix(in srgb, var(--c) 30%, transparent);
  transition: all 0.2s;
}

.site-card:hover {
  border-color: color-mix(in srgb, var(--c) 60%, transparent);
  box-shadow: 0 8px 24px -12px color-mix(in srgb, var(--c) 45%, transparent);
  transform: translateY(-2px);
}
</style>
