/**
 * 第三方 skill 市场接口（/api/v1/skill/market/*）。
 * 链接导入 + 外部市场目录 CRUD（site/*）；站内搜索/一键同步已下线（一律目录引导+链接导入）。
 * 响应壳 {code:0, data:...}，requestClient 已返回 data 字段。
 */
import { requestClient } from '#/api/request';

export namespace SkillMarketApi {
  /** 链接导入请求（zip 直链或详情页链接；可一并记原作者/打标签）。 */
  export interface ImportReq {
    url: string;
    namespaceId: string;
    upstreamAuthor?: string;
    tags?: string[];
  }

  export interface ImportResult {
    skillId: string;
    versionId: string;
  }

  /** 外部市场目录项（后端 entity.SkillMarketSite，aa_skill_market_site）。 */
  export interface Site {
    marketId: string;
    name: string;
    siteUrl: string;
    description: string;
    icon: string; // emoji 兜底
    logoUrl: string; // logo 图片 URL（空则用 icon）
    themeColor: string;
    status: number; // 1=显示 0=隐藏
    sort: number;
    createdAt?: number;
    updatedAt?: number;
  }

  export interface SiteListParams {
    keyword?: string;
    status?: string;
    pageNum?: number;
    pageSize?: number;
  }

  export interface SiteListResult {
    list: Site[];
    total: number;
  }

  /** 目录新建/编辑请求。 */
  export interface SiteReq {
    marketId?: string; // 编辑必填
    name: string;
    siteUrl: string;
    description?: string;
    icon?: string;
    logoUrl?: string;
    themeColor?: string;
    status?: number;
    sort?: number;
  }
}

const BASE = '/api/v1/skill/market';

/** 链接导入：zip 直链或详情页链接 → 保存到本地技能库（记原作者/标签溯源）。 */
export function importMarketUrlApi(data: SkillMarketApi.ImportReq) {
  return requestClient.post<SkillMarketApi.ImportResult>(`${BASE}/import`, data);
}

/** 启用中的外部市场目录全量（第三方市场页卡片展示）。 */
export function listMarketSitesApi() {
  return requestClient.get<{ list: SkillMarketApi.Site[] }>(`${BASE}/site/all`);
}

/** 外部市场目录分页列表（目录管理）。 */
export function listMarketSitePageApi(params: SkillMarketApi.SiteListParams) {
  return requestClient.get<SkillMarketApi.SiteListResult>(`${BASE}/site/list`, {
    params,
  });
}

/** 新增外部市场目录项。 */
export function addMarketSiteApi(data: SkillMarketApi.SiteReq) {
  return requestClient.post<{ marketId: string }>(`${BASE}/site/add`, data);
}

/** 编辑外部市场目录项。 */
export function editMarketSiteApi(data: SkillMarketApi.SiteReq) {
  return requestClient.post(`${BASE}/site/edit`, data);
}

/** 删除外部市场目录项。 */
export function deleteMarketSiteApi(marketId: string) {
  return requestClient.post(`${BASE}/site/delete`, { marketId });
}
