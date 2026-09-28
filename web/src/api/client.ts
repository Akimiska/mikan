import createClient, { type Middleware } from "openapi-fetch";
import type { components, paths } from "./schema";

export type Schemas = components["schemas"];
export type User = Schemas["UserView"];
export type Tariff = Schemas["TariffView"];
export type Inbound = Schemas["InboundView"];
export type Preset = Schemas["Info"];
export type NodeView = Schemas["NodeView"];
export type Overview = Schemas["OverviewOutputBody"];
export type SettingsView = Schemas["SettingsView"];
export type TrafficPoint = Schemas["TrafficPoint"];
export type Me = Schemas["MeBody"];
export type UserState = User["state"];

// The server injects <base href="/<secret>/">; everything is relative to it.
const base = new URL("./", document.baseURI);
export const basePath = base.pathname.replace(/\/$/, "");

let csrfToken = "";
export function setCsrf(token: string) {
  csrfToken = token;
}

export class ApiError extends Error {
  status: number;
  detail: string;
  fields: Record<string, string>;
  retryAfter: number;

  constructor(status: number, body: unknown, retryAfter = 0) {
    const b = (body ?? {}) as Schemas["ErrorModel"];
    super(b.detail || b.title || `HTTP ${status}`);
    this.status = status;
    this.detail = b.detail ?? "";
    this.retryAfter = retryAfter;
    this.fields = {};
    for (const e of b.errors ?? []) {
      if (e.location) this.fields[e.location.replace(/^body\./, "")] = e.message ?? "";
    }
  }
}

const session: Middleware = {
  onRequest({ request }) {
    if (request.method !== "GET" && request.method !== "HEAD" && csrfToken) {
      request.headers.set("X-CSRF-Token", csrfToken);
    }
    return request;
  },
  onResponse({ response, request }) {
    if (response.status === 401 && !request.url.endsWith("/auth/login")) {
      window.dispatchEvent(new Event("mikan:unauthorized"));
    }
    return response;
  },
};

export const api = createClient<paths>({ baseUrl: base.origin + basePath });
api.use(session);

type Result<T> = { data?: T; error?: unknown; response: Response };

export async function unwrap<T>(p: Promise<Result<T>>): Promise<T> {
  let res: Result<T>;
  try {
    res = await p;
  } catch {
    throw new ApiError(0, { detail: "network" });
  }
  if (!res.response.ok) {
    throw new ApiError(res.response.status, res.error, Number(res.response.headers.get("Retry-After") ?? 0));
  }
  return res.data as T;
}

/** Human-readable message for errors the UI does not handle specially. */
export function errorText(e: unknown): string {
  if (!(e instanceof ApiError)) return "Что-то пошло не так. Попробуйте ещё раз.";
  if (e.status === 0) return "Нет связи с панелью. Проверьте интернет.";
  if (e.status === 503 && e.detail === "no_free_slots") return "Закончились слоты, пополняем пул — повторите через минуту.";
  if (e.status === 503) return "Нода не отвечает. VPN у пользователей при этом может работать.";
  if (e.status === 403) return "Сессия устарела. Обновите страницу.";
  if (e.status === 404) return "Не найдено — возможно, уже удалено.";
  if (e.status === 409 || e.status === 422) return Object.values(e.fields)[0] || "Проверьте введённые данные.";
  if (e.status === 429) return `Слишком много попыток. Подождите ${e.retryAfter || 60} с.`;
  return "Ошибка сервера. Попробуйте ещё раз.";
}
