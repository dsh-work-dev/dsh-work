import {Check, ChevronLeft, ChevronRight, createElement, LoaderCircle, TriangleAlert, type IconNode} from "lucide";

// One maintained icon family (lucide). Only icons in use are imported so the
// bundle stays small; add names here rather than drawing ad-hoc glyphs.
const icons = {
  check: Check,
  "chevron-left": ChevronLeft,
  "chevron-right": ChevronRight,
  loader: LoaderCircle,
  "triangle-alert": TriangleAlert,
} satisfies Record<string, IconNode>;

export type IconName = keyof typeof icons;

/** Decorative icon; the surrounding text carries the meaning. */
export function icon(name: IconName): SVGElement {
  const element = createElement(icons[name], {class: "icon", "aria-hidden": "true", focusable: "false"});
  return element;
}

/** Replace static `<span data-icon="name">` placeholders in markup. */
export function hydrateIcons(root: ParentNode) {
  for (const placeholder of Array.from(root.querySelectorAll<HTMLElement>("[data-icon]"))) {
    const name = placeholder.dataset.icon as IconName;
    if (name in icons) placeholder.replaceWith(icon(name));
  }
}
