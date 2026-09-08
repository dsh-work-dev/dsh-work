/** Disable duplicate submissions without dropping keyboard focus after a save. */
export function beginControlUpdate(control: HTMLInputElement | HTMLSelectElement) {
  const hadFocus = document.activeElement === control;
  control.disabled = true;
  return () => {
    control.disabled = false;
    if (hadFocus && document.activeElement === document.body && document.hasFocus()) {
      control.focus({preventScroll: true});
    }
  };
}
