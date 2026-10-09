/** One short status line under the shell bar; a new message replaces the previous one. */
export function createToast(): {element: HTMLElement; show(text: string): void} {
  const element = document.createElement("div");
  element.className = "shell-toast";
  element.setAttribute("role", "status");
  element.hidden = true;
  let timer: number | undefined;
  return {
    element,
    show(text: string) {
      window.clearTimeout(timer);
      element.textContent = text;
      element.hidden = false;
      timer = window.setTimeout(() => { element.hidden = true; }, 3000);
    },
  };
}
