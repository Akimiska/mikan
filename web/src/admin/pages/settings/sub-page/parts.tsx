// What the sections of the subscription page's editor share: the page's types, its limits
// and the card each section is.
import type { Dispatch, ReactNode, SetStateAction } from "react";
import { basePath, type Schemas } from "../../../../api/client";
import { Card as LayoutCard } from "../../../../components/layout";

export type View = Schemas["SubPageView"];

export type Config = Schemas["Page"];

export type Block = Schemas["PageBlock"];

export type Look = Schemas["PageLook"];

export type Asset = Schemas["PageAsset"];

export type Form = { config: Config; brand_accent: string };

export type Set = (f: (c: Config) => Config) => void;

export const COLOR = /^#[0-9A-Fa-f]{6}$/;

export const LIMITS = { logo: 512, background: 2048 } as const;

export const MAX_CUSTOM = 12;

export const MAX_CSS = 20480;

export const imageURL = (a?: Asset) => (a ? `${basePath}/api/v1/sub-page/images/${a.name}?v=${a.hash}` : undefined);

export type SectionProps = { draft: Form; setDraft: Dispatch<SetStateAction<Form>>; set: Set; errors: Record<string, string> };

// The editor's cards come and go with its sections: no entrance.
export function Card({ title, sub, children }: { title: string; sub?: string; children: ReactNode }) {
  return (
    <LayoutCard title={title} sub={sub} reveal={false}>
      {children}
    </LayoutCard>
  );
}
