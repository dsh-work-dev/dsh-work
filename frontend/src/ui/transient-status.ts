const timers = new WeakMap<HTMLElement, number>();

export function transientStatus(element: HTMLElement, message: string) {
  const previous = timers.get(element);
  if (previous !== undefined) window.clearTimeout(previous);
  element.textContent = message;
  timers.set(element, window.setTimeout(() => {
    if (element.textContent === message) element.textContent = "";
    timers.delete(element);
  }, 5000));
}
