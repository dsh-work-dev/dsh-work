import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import test from "node:test";

const html = readFileSync(new URL("../index.html", import.meta.url), "utf8").replace(/<!--[\s\S]*?-->/g, "");

function parentElements(tagName: string, id: string): Array<{tag: string; id: string}> | undefined {
  const stack: Array<{tag: string; id: string}> = [];
  const tags = /<\/?(main|section|div|dialog)\b([^>]*)>/g;
  let match: RegExpExecArray | null;
  while ((match = tags.exec(html)) !== null) {
    const [, tag, attributes] = match;
    if (match[0][1] === "/") {
      let index = stack.length - 1;
      while (index >= 0 && stack[index].tag !== tag) index--;
      if (index >= 0) stack.length = index;
      continue;
    }

    const elementID = /\bid="([^"]+)"/.exec(attributes)?.[1] ?? "";
    if (tag === tagName && elementID === id) return [...stack];
    stack.push({tag, id: elementID});
  }
  return undefined;
}

test("profile migration modal stays outside the hidden profile detail pane", () => {
  const parents = parentElements("dialog", "profile-migrate-dialog");
  assert.ok(parents, "profile migration dialog must exist");
  assert.deepEqual(parents.at(-1), {tag: "section", id: "manager-section-profiles"});
  assert.ok(!parents.some(element => element.id === "manager-profile-detail"));
});
